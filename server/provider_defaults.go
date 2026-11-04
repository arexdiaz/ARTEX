package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

type profileCatalog struct {
	Models  []string
	Expires time.Time
}

func (s *Server) modelsForProfile(ctx context.Context, p *db.LLMProfile, refresh ...bool) ([]string, error) {
	if p.Model != "" {
		return []string{p.Model}, nil
	}
	if p.Format != "ollama" && p.APIKey == "" {
		return nil, fmt.Errorf("provider %q has no API key", p.Name)
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s", p.ID, p.Format, p.BaseURL, p.Proxy, p.APIKey))))
	s.catalogMu.Lock()
	cached, ok := s.catalogCache[key]
	s.catalogMu.Unlock()
	if ok && time.Now().Before(cached.Expires) && (len(refresh) == 0 || !refresh[0]) {
		return slices.Clone(cached.Models), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	models, err := listProviderModels(ctx, p.Format, p.BaseURL, p.APIKey, p.Proxy)
	if err != nil {
		return nil, fmt.Errorf("discover models for %q: %w", p.Name, err)
	}
	s.catalogMu.Lock()
	if s.catalogCache == nil {
		s.catalogCache = map[string]profileCatalog{}
	}
	s.catalogCache[key] = profileCatalog{Models: slices.Clone(models), Expires: time.Now().Add(time.Minute)}
	s.catalogMu.Unlock()
	return models, nil
}

func (s *Server) validateProviderModel(ctx context.Context, p *db.LLMProfile, model string) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("select a default model for provider %q", p.Name)
	}
	models, err := s.modelsForProfile(ctx, p)
	if err != nil {
		return err
	}
	if !slices.Contains(models, model) {
		models, err = s.modelsForProfile(ctx, p, true)
		if err != nil {
			return err
		}
	}
	if !slices.Contains(models, model) {
		return fmt.Errorf("model %q is not available from provider %q", model, p.Name)
	}
	return nil
}

// TaskModelRequest allows either a provider's default or a concrete model/effort.
// nil effort inherits the provider; empty effort uses the model default; off disables reasoning.
type taskModelRequest struct {
	Model           string  `json:"llm_model"`
	ReasoningEffort *string `json:"reasoning_effort"`
	Source          string  `json:"llm_source"`
}

func (s *Server) resolveTaskModels(ctx context.Context, ids []int64, request taskModelRequest, previous ...[]db.TaskModelSelection) ([]int64, []db.TaskModelSelection, error) {
	if request.Source != "" && request.Source != "system_default" {
		return nil, nil, fmt.Errorf("invalid LLM source")
	}
	if request.Source == "system_default" && (len(ids) > 0 || request.Model != "" || request.ReasoningEffort != nil) {
		return nil, nil, fmt.Errorf("system_default cannot be combined with provider, model or reasoning overrides")
	}
	if len(ids) > 1 && (request.Model != "" || request.ReasoningEffort != nil) {
		return nil, nil, fmt.Errorf("model and reasoning overrides require a single provider")
	}
	if len(ids) == 0 {
		active, err := s.m.pg.ActiveProfile()
		if err != nil {
			return nil, nil, err
		}
		if active == nil {
			return nil, nil, fmt.Errorf("no system default provider: select a provider and default model in System → LLM")
		}
		ids = []int64{active.ID}
	}
	var selections []db.TaskModelSelection
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, nil, fmt.Errorf("invalid or duplicate LLM provider id")
		}
		seen[id] = true
		p, err := s.m.pg.ProfileByID(id)
		if err != nil {
			return nil, nil, err
		}
		if p == nil {
			return nil, nil, fmt.Errorf("provider #%d not found", id)
		}
		// Reordering a task chain or moving its cursor preserves saved choices.
		if len(previous) > 0 && request.Source == "" && request.Model == "" && request.ReasoningEffort == nil {
			var saved *db.TaskModelSelection
			for _, choice := range previous[0] {
				if choice.ProfileID == id {
					selected := choice
					saved = &selected
					break
				}
			}
			if saved != nil {
				if err := s.validateProviderModel(ctx, p, saved.Model); err != nil {
					return nil, nil, err
				}
				if _, _, ok := s.providerForTaskSelection(previous[0], id); !ok {
					return nil, nil, fmt.Errorf("saved task model for %q is unavailable", p.Name)
				}
				selections = append(selections, *saved)
				continue
			}
		}
		model := strings.TrimSpace(request.Model)
		if model == "" {
			model = p.ResolvedModel()
		}
		if err := s.validateProviderModel(ctx, p, model); err != nil {
			return nil, nil, err
		}
		cfg, _, ok := s.loadChatProfileConfig(id, model, request.ReasoningEffort)
		if !ok {
			return nil, nil, fmt.Errorf("provider %q or reasoning setting is not usable", p.Name)
		}
		selections = append(selections, db.TaskModelSelection{ProfileID: id, Model: cfg.Model, ReasoningEffort: cfg.ReasoningEffort, ThinkingType: cfg.ThinkingType})
	}
	return ids, selections, nil
}

func (s *Server) providerForTaskSelection(ids []db.TaskModelSelection, id int64) (llm.Provider, agent.Config, bool) {
	for _, selected := range ids {
		if selected.ProfileID == id {
			return s.providerForChatModel(id, selected.Model, &selected.ReasoningEffort, selected.ThinkingType)
		}
	}
	return s.providerForProfile(id)
}

package server

import (
	"fmt"
	"net/http"

	"github.com/Autumn-27/artex/db"
)

type taskLLMResolution struct {
	ProfileID       *int64 `json:"profile_id,omitempty"`
	Name            string `json:"name"`
	Format          string `json:"format"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	ThinkingType    string `json:"thinking_type"`
	Source          string `json:"source"`
	Available       bool   `json:"available"`
	Reason          string `json:"reason,omitempty"`
}

func (s *Server) resolutionFromProfile(p *db.LLMProfile, source string) taskLLMResolution {
	if p == nil {
		return taskLLMResolution{Source: source, Reason: "LLM profile does not exist"}
	}
	id := p.ID
	result := taskLLMResolution{
		ProfileID:       &id,
		Name:            p.Name,
		Format:          p.Format,
		Model:           p.ResolvedModel(),
		ReasoningEffort: p.ReasoningEffort, ThinkingType: p.ThinkingType,
		Source: source,
	}
	// Ollama profiles are keyless by design; the other formats need a key.
	if p.APIKey == "" && p.Format != "ollama" {
		result.Reason = "LLM profile has no API Key set"
		return result
	}
	if _, _, ok := s.providerForProfile(p.ID); !ok {
		result.Reason = "LLM profile format or parameters are invalid"
		return result
	}
	result.Available = true
	return result
}

// resolveTaskRoleLLM mirrors taskLLMRuntime.current, including saved task choices.
// Database failures are returned rather than represented as an unavailable model.
func (s *Server) resolveTaskRoleLLM(t *Task, agentKey string) (taskLLMResolution, error) {
	if s == nil || s.m == nil || s.m.pg == nil {
		return taskLLMResolution{}, fmt.Errorf("database unavailable")
	}
	state := t.llmStateSnapshot()
	a, err := s.m.pg.GetAgentByKey(agentKey)
	if err != nil {
		return taskLLMResolution{}, fmt.Errorf("load agent %q: %w", agentKey, err)
	}
	if a != nil && a.LLMProfileID != nil && len(state.Selections) == 0 {
		p, err := s.m.pg.ProfileByID(*a.LLMProfileID)
		if err != nil {
			return taskLLMResolution{}, fmt.Errorf("load agent LLM profile: %w", err)
		}
		if p != nil {
			resolved := s.resolutionFromProfile(p, "agent_binding")
			if resolved.Available {
				return resolved, nil
			}
		}
	}
	if len(state.ProfileIDs) > 0 {
		if state.ActiveID == nil {
			return taskLLMResolution{Source: "task_chain", Reason: "task LLM profile chain quota exhausted"}, nil
		}
		p, err := s.m.pg.ProfileByID(*state.ActiveID)
		if err != nil {
			return taskLLMResolution{}, fmt.Errorf("load task LLM profile: %w", err)
		}
		if len(state.Selections) > 0 && p != nil {
			_, cfg, ok := s.providerForTaskSelection(state.Selections, p.ID)
			result := taskLLMResolution{ProfileID: &p.ID, Name: p.Name, Format: p.Format, Model: cfg.Model, ReasoningEffort: cfg.ReasoningEffort, ThinkingType: cfg.ThinkingType, Source: "task_chain", Available: ok}
			if !ok {
				result.Reason = "saved task model is unavailable"
			}
			return result, nil
		}
		return s.resolutionFromProfile(p, "task_chain"), nil
	}
	p, err := s.m.pg.ActiveProfile()
	if err != nil {
		return taskLLMResolution{}, fmt.Errorf("load global LLM profile: %w", err)
	}
	if p != nil {
		resolved := s.resolutionFromProfile(p, "global_profile")
		if resolved.Available {
			return resolved, nil
		}
	}
	s.cfgMu.Lock()
	on, name, cfg := s.llmOn, s.llmProf, s.llmCfg
	s.cfgMu.Unlock()
	if on {
		if name == "" {
			name = "global configuration"
		}
		return taskLLMResolution{
			Name: name, Format: cfg.Provider(), Model: cfg.Model,
			ReasoningEffort: cfg.ReasoningEffort, ThinkingType: cfg.ThinkingType,
			Source: "environment", Available: true,
		}, nil
	}
	return taskLLMResolution{Source: "global", Reason: "no available LLM profile"}, nil
}

func (s *Server) taskLLMResolutionHandler(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	roles := map[string]taskLLMResolution{}
	for responseKey, agentKey := range map[string]string{
		"mainagent": "mainagent",
		"planner":   "planner",
		"worker":    "worker",
	} {
		resolved, err := s.resolveTaskRoleLLM(t, agentKey)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		roles[responseKey] = resolved
	}
	writeJSON(w, http.StatusOK, roles)
}

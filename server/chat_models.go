package server

import (
	"context"
	"errors"
	"iter"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/llmrec"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
)

func validChatEffort(effort string) bool {
	switch effort {
	case "", "off", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}

func (s *Server) activeProfileNeedsModel() bool {
	if s.m == nil || s.m.pg == nil {
		return false
	}
	p, err := s.m.pg.ActiveProfile()
	return err == nil && p != nil && strings.TrimSpace(p.Model) == ""
}

func (s *Server) validateConversationModel(id *int64, choice *db.ConversationModel) error {
	choice.Model = strings.TrimSpace(choice.Model)
	if utf8.RuneCountInString(choice.Model) > 256 || strings.IndexFunc(choice.Model, unicode.IsControl) >= 0 {
		return errors.New("model must be at most 256 characters without control characters")
	}
	if choice.ReasoningEffort != nil && !validChatEffort(*choice.ReasoningEffort) {
		return errors.New("invalid reasoning effort")
	}
	if id == nil {
		if choice.Model != "" || choice.ReasoningEffort != nil {
			return errors.New("select a profile for this model and reasoning choice")
		}
		return nil
	}
	if _, _, ok := s.loadChatProfileConfig(*id, choice.Model, choice.ReasoningEffort); !ok {
		return errors.New("select a usable LLM profile and model; automatic-discovery profiles require a model selection")
	}
	return nil
}

func (s *Server) loadChatProfileConfig(id int64, model string, effort *string) (agent.Config, string, bool) {
	p, err := s.m.pg.ProfileByID(id)
	if err != nil || p == nil {
		return agent.Config{}, "", false
	}
	configured := strings.TrimSpace(p.Model)
	if model == "" {
		model = configured
	}
	if model == "" || (configured != "" && model != configured) {
		return agent.Config{}, "", false
	}
	cfg := agent.ConfigFrom(p.Format, model, p.BaseURL, p.APIKey, p.Proxy)
	cfg.RatePerSecond, cfg.RatePerMinute = p.RatePerSecond, p.RatePerMinute
	cfg.ContextWindowK = p.ContextWindowK
	cfg.ThinkingType, cfg.ReasoningEffort = p.ThinkingType, p.ReasoningEffort
	if effort != nil {
		if !validChatEffort(*effort) {
			return cfg, p.Name, false
		}
		cfg.ReasoningEffort = *effort
		if *effort == "off" {
			cfg.ThinkingType, cfg.ReasoningEffort = "disabled", ""
		}
		if *effort != "" && *effort != "off" && *effort != "none" && cfg.ThinkingType == "disabled" {
			cfg.ThinkingType = ""
		}
	}
	cfg.Stream = p.Streaming
	cfg.MaxTokens, cfg.MaxTokensField = p.MaxTokens, p.MaxTokensField
	cfg.SessionHeaderKey = p.SessionHeaderKey
	s.applyProfileRetry(&cfg, p)
	return cfg, p.Name, cfg.APIKey != "" || !cfg.NeedsAPIKey()
}

type profileModelKey struct {
	ID                            int64
	Model, Effort, Thinking       string
	ExplicitModel, ExplicitEffort bool
}
type profileProviderContextKey struct{}

// Every model/effort variant of a connection passes through the same limiter.
// The concrete provider travels in context so concurrent chats never mutate it.
type profileProviderDispatch struct{}

func (profileProviderDispatch) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return ctx.Value(profileProviderContextKey{}).(llm.Provider).Stream(ctx, req)
}
func (profileProviderDispatch) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return ctx.Value(profileProviderContextKey{}).(llm.Provider).Complete(ctx, req)
}

type profileModelProvider struct{ concrete, shared llm.Provider }

func (p profileModelProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return p.shared.Stream(context.WithValue(ctx, profileProviderContextKey{}, p.concrete), req)
}
func (p profileModelProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return p.shared.Complete(context.WithValue(ctx, profileProviderContextKey{}, p.concrete), req)
}

func (s *Server) providerForChatModel(id int64, model string, effort *string) (llm.Provider, agent.Config, bool) {
	s.provCacheMu.Lock()
	generation := s.provCacheGen
	s.provCacheMu.Unlock()
	cfg, name, ok := s.loadChatProfileConfig(id, model, effort)
	if !ok {
		return nil, cfg, false
	}
	key := profileModelKey{id, cfg.Model, cfg.ReasoningEffort, cfg.ThinkingType, model != "", effort != nil}
	s.provCacheMu.Lock()
	if cached := s.provByModel[key]; cached != nil && generation == s.provCacheGen {
		s.provCacheMu.Unlock()
		return cached.prov, cached.cfg, true
	}
	s.provCacheMu.Unlock()
	wire := cfg
	wire.RatePerSecond, wire.RatePerMinute = 0, 0
	prov, err := wire.NewProvider()
	if err != nil {
		return nil, cfg, false
	}
	s.provCacheMu.Lock()
	if generation != s.provCacheGen {
		s.provCacheMu.Unlock()
		return s.providerForChatModel(id, model, effort)
	}
	defer s.provCacheMu.Unlock()
	if cached := s.provByModel[key]; cached != nil {
		return cached.prov, cached.cfg, true
	}
	if s.profileLimits == nil {
		s.profileLimits = map[int64]llm.Provider{}
	}
	shared := s.profileLimits[id]
	if shared == nil {
		shared = llm.RateLimited(profileProviderDispatch{}, llm.RateLimit{PerSecond: cfg.RatePerSecond, PerMinute: cfg.RatePerMinute})
		s.profileLimits[id] = shared
	}
	prov = profileModelProvider{concrete: prov, shared: shared}
	prov = llmrec.Wrap(prov, s.m.PG(), cfg.Model, name, cfg.ThinkingType, cfg.ReasoningEffort, s.m.LLMRecordEnabled)
	identity := sideModel(cfg, id, name)
	if model != "" || effort != nil {
		identity.Selection = &sidequestion.ModelSelection{Model: model, ReasoningEffort: effort}
	}
	prov = sidequestion.Bind(prov, identity)
	entry := &provEntry{prov: prov, cfg: cfg}
	if s.provByModel == nil {
		s.provByModel = map[profileModelKey]*provEntry{}
	}
	s.provByModel[key] = entry
	if model == "" && effort == nil {
		if s.provByProfile == nil {
			s.provByProfile = map[int64]*provEntry{}
		}
		s.provByProfile[id] = entry
	}
	return prov, cfg, true
}

func (s *Server) chatAgentWithModel(prov llm.Provider, cfg agent.Config) *agent.ChatAgent {
	tx := transcript.NewStore(filepath.Join(s.m.dir, "transcripts"))
	ca := agent.NewChatAgent(prov, cfg.Model, s.m.dir, tx, cfg.CompactionWindow())
	ca.SetProxy(s.m.ProxyAddr(), s.m.ProxyCACert())
	ca.SetWebSearch(s.m.WebSearchOpts())
	ca.SetGuard(s.chatGuard())
	ca.SetNonStreaming(nonStreamingResolver(cfg))
	ca.SetMaxTokens(maxTokensResolver(cfg))
	ca.SetNoaEnabled(s.m.NoaCompactionEnabled)
	return ca
}

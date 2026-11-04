package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
)

func modelTestServer(t *testing.T) *Server {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
}

func TestConversationModelPersistenceAndValidation(t *testing.T) {
	s := modelTestServer(t)
	id, err := s.m.pg.SaveProfile(&db.LLMProfile{Name: fmt.Sprintf("discover-%d", time.Now().UnixNano()), Format: "openai", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.m.pg.DeleteProfile(id) })
	if _, ok := s.loadProfileConfig(id); ok {
		t.Fatal("blank model silently acquired a provider default")
	}
	bad := db.ConversationModel{}
	if s.validateConversationModel(&id, &bad) == nil {
		t.Fatal("discovery profile accepted without a model")
	}
	if s.validateConversationModel(nil, &db.ConversationModel{Model: "chosen"}) == nil {
		t.Fatal("model without profile accepted")
	}
	invalid := "unsupported"
	if s.validateConversationModel(&id, &db.ConversationModel{Model: "chosen", ReasoningEffort: &invalid}) == nil {
		t.Fatal("invalid effort accepted")
	}
	response := postJSON(t, s.pgCreateConversation, "/api/conversations", map[string]any{"agent_key": "mainagent", "llm_profile_id": id, "llm_model": "vendor/model-a", "reasoning_effort": "high"})
	conversationID, ok := response["id"].(float64)
	if !ok {
		t.Fatalf("create: %v", response)
	}
	t.Cleanup(func() { _ = s.m.pg.DeleteConversation(int64(conversationID)) })
	conversation, err := s.m.pg.GetConversation(int64(conversationID))
	if err != nil {
		t.Fatal(err)
	}
	if conversation.LLMModel != "vendor/model-a" || conversation.ReasoningEffort == nil || *conversation.ReasoningEffort != "high" {
		t.Fatalf("choice not persisted: %+v", conversation)
	}
	if s.resolveChatAgent(conversation) == nil {
		t.Fatal("discovered model did not resolve without an active global profile")
	}
	for _, effort := range []*string{new(string), nil} {
		if err := s.m.pg.UpdateConversationModel(conversation.ID, &id, db.ConversationModel{Model: "vendor/model-b", ReasoningEffort: effort}); err != nil {
			t.Fatal(err)
		}
		loaded, _ := s.m.pg.GetConversation(conversation.ID)
		if !reflect.DeepEqual(loaded.ReasoningEffort, effort) {
			t.Fatalf("inherit vs explicit empty lost: got %v want %v", loaded.ReasoningEffort, effort)
		}
	}
	body := bytes.NewBufferString(`{"llm_profile_id":null}`)
	request := httptest.NewRequest(http.MethodPatch, "/api/conversations/1/profile", body)
	request.SetPathValue("id", fmt.Sprint(conversation.ID))
	recorder := httptest.NewRecorder()
	s.pgUpdateConversation(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("clear: %s", recorder.Body.String())
	}
	loaded, _ := s.m.pg.GetConversation(conversation.ID)
	if loaded.LLMProfileID != nil || loaded.LLMModel != "" || loaded.ReasoningEffort != nil {
		t.Fatalf("default did not clear all overrides: %+v", loaded)
	}
	if err := s.m.pg.UpdateConversationModel(loaded.ID, &id, db.ConversationModel{Model: "chosen", ReasoningEffort: &invalid}); err != nil {
		t.Fatal(err)
	}
	if err := s.m.pg.DeleteProfile(id); err != nil {
		t.Fatal(err)
	}
	loaded, _ = s.m.pg.GetConversation(loaded.ID)
	if loaded.LLMProfileID != nil || loaded.LLMModel != "" || loaded.ReasoningEffort != nil {
		t.Fatal("deleted provider left an orphaned model choice")
	}
}

func TestChatModelWireIsolationAndSharedLimiter(t *testing.T) {
	s := modelTestServer(t)
	var mu sync.Mutex
	var requests []map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		mu.Lock()
		requests = append(requests, payload)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"1","choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer fake.Close()
	id, err := s.m.pg.SaveProfile(&db.LLMProfile{Name: fmt.Sprintf("wire-%d", time.Now().UnixNano()), Format: "openai", BaseURL: fake.URL, APIKey: "test-key", RatePerSecond: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.m.pg.DeleteProfile(id) })
	high, low := "high", "low"
	a, cfgA, ok := s.providerForChatModel(id, "vendor/model-a", &high)
	if !ok {
		t.Fatal("model a unavailable")
	}
	b, cfgB, ok := s.providerForChatModel(id, "vendor/model-b", &low)
	if !ok || cfgA.Model == cfgB.Model || cfgA.ReasoningEffort == cfgB.ReasoningEffort {
		t.Fatal("model configs leaked across variants")
	}
	req := llm.CompletionRequest{Messages: []llm.Message{llm.UserText("hello")}}
	if _, _, _, err := a.Complete(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, _, _, err := b.Complete(ctx, req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("variant bypassed shared rate limit: %v", err)
	}
	mu.Lock()
	if len(requests) != 1 || requests[0]["model"] != "vendor/model-a" || requests[0]["reasoning_effort"] != "high" {
		t.Fatalf("wrong request: %v", requests)
	}
	mu.Unlock()
	identity := sideModel(cfgA, id, "wire")
	identity.Selection = &sidequestion.ModelSelection{Model: cfgA.Model, ReasoningEffort: &high}
	if _, err := s.sideProvider(identity); err != nil {
		t.Fatalf("side question lost selected model: %v", err)
	}
	off := "off"
	_, offConfig, ok := s.providerForChatModel(id, "vendor/model-b", &off)
	if !ok || offConfig.ThinkingType != "disabled" || offConfig.ReasoningEffort != "" {
		t.Fatalf("off reasoning not normalized: %+v", offConfig)
	}
	_, err = s.m.pg.Exec(`UPDATE llm_profiles SET model='fixed-model' WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	s.invalidateProfileAgents()
	if _, err := s.sideProvider(identity); err == nil {
		t.Fatal("side question accepted model after profile changed")
	}
	if _, _, ok := s.providerForChatModel(id, "other-model", nil); ok {
		t.Fatal("explicit-model profile accepted arbitrary model")
	}
}

func TestModelCatalogPaginationAndDuplicates(t *testing.T) {
	var paths []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.String())
		if r.Header.Get("x-api-key") != "catalog-key" {
			t.Error("missing saved credential")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "b" {
			fmt.Fprint(w, `{"data":[{"id":"b"},{"id":"c"}],"has_more":false}`)
		} else {
			fmt.Fprint(w, `{"data":[{"id":"a"},{"id":"b"}],"has_more":true,"last_id":"b"}`)
		}
	}))
	defer fake.Close()
	models, err := fetchModelCatalog(t.Context(), fake.Client(), fake.URL+"/v1/models", http.Header{"X-Api-Key": []string{"catalog-key"}})
	if err != nil || strings.Join(models, ",") != "a,b,c" || len(paths) != 2 {
		t.Fatalf("models=%v paths=%v err=%v", models, paths, err)
	}
}

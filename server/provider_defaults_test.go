package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

func TestProviderDefaultsAndTaskModelOverrides(t *testing.T) {
	s := modelTestServer(t)
	var wire map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"data":[{"id":"demo/a"},{"id":"demo/b"}]}`)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&wire)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(fake.Close)
	p := &db.LLMProfile{Name: fmt.Sprintf("defaults-%d", time.Now().UnixNano()), Format: "openai", BaseURL: fake.URL, APIKey: "test-secret", ReasoningEffort: "high", ThinkingType: "enabled"}
	id, err := s.m.pg.SaveProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = id
	old, _ := s.m.pg.ActiveProfile()
	t.Cleanup(func() {
		_, _ = s.m.pg.Exec(`UPDATE llm_profiles SET is_default=false WHERE id=$1`, id)
		if old != nil {
			_ = s.m.pg.SetActiveProfile(old.ID)
		}
		_ = s.m.pg.DeleteProfile(id)
	})
	activate := func() *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{"id": id})
		r := httptest.NewRecorder()
		s.pgActivateProfile(r, httptest.NewRequest(http.MethodPost, "/api/llm/profiles/active", bytes.NewReader(b)))
		return r
	}
	setDefault := func(model string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"default_model": model})
		req := httptest.NewRequest(http.MethodPut, "/api/llm/profiles/1/default-model", bytes.NewReader(b))
		req.SetPathValue("id", fmt.Sprint(id))
		r := httptest.NewRecorder()
		s.pgSetProfileDefaultModel(r, req)
		return r
	}
	if r := activate(); r.Code != 400 {
		t.Fatalf("activated without default: %d %s", r.Code, r.Body.String())
	}
	if err := s.m.pg.SetActiveProfile(id); err == nil {
		t.Fatal("DB accepted active blank model")
	}
	if r := setDefault("missing"); r.Code != 400 {
		t.Fatalf("accepted missing model: %s", r.Body.String())
	}
	if r := setDefault("demo/a"); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	stored, _ := s.m.pg.ProfileByID(id)
	if stored.Model != "" || stored.DefaultModel != "demo/a" || stored.ReasoningEffort != "high" || stored.APIKey != p.APIKey {
		t.Fatalf("default changed provider settings: %+v", stored)
	}
	models, err := s.modelsForProfile(t.Context(), stored)
	if err != nil || len(models) != 2 {
		t.Fatalf("default narrowed discovery: %v %v", models, err)
	}
	if r := activate(); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	stored.DefaultModel = ""
	if _, err := s.m.pg.SaveProfile(stored); err == nil {
		t.Fatal("active provider lost default")
	}
	if r := setDefault(""); r.Code != 400 {
		t.Fatal("API cleared active default")
	}

	for _, tc := range []struct {
		name, model              string
		effort                   *string
		wantEffort, wantThinking string
	}{
		{"provider defaults", "", nil, "high", "enabled"},
		{"model inherits reasoning", "demo/b", nil, "high", "enabled"},
		{"override reasoning", "demo/b", strPtr("low"), "low", "enabled"},
		{"model default reasoning", "demo/b", strPtr(""), "", ""},
		{"reasoning off", "demo/b", strPtr("off"), "", "disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, selections, err := s.resolveTaskModels(t.Context(), []int64{id}, taskModelRequest{Model: tc.model, ReasoningEffort: tc.effort})
			if err != nil {
				t.Fatal(err)
			}
			wantModel := tc.model
			if wantModel == "" {
				wantModel = "demo/a"
			}
			if len(selections) != 1 || selections[0].Model != wantModel || selections[0].ReasoningEffort != tc.wantEffort || selections[0].ThinkingType != tc.wantThinking {
				t.Fatalf("wrong selection: %+v", selections)
			}
		})
	}
	ids, choices, err := s.resolveTaskModels(t.Context(), nil, taskModelRequest{Source: "system_default"})
	if err != nil || !reflect.DeepEqual(ids, []int64{id}) || choices[0].Model != "demo/a" {
		t.Fatalf("system default: %v %v %v", ids, choices, err)
	}
	task, err := s.m.CreateTaskWithOptions("saved task default", "keep chosen model", db.TaskCreateOptions{LLMProfileIDs: ids, LLMSelections: choices})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.m.DeleteTask(task.ID, DeleteTaskOptions{}) })
	p.DefaultModel, p.ReasoningEffort, p.ThinkingType = "demo/b", "low", "disabled"
	if _, err := s.m.pg.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	s.invalidateProfileAgents()
	s.reapplyActiveProfile()
	boundID, err := s.m.pg.SaveProfile(&db.LLMProfile{Name: fmt.Sprintf("bound-%d", time.Now().UnixNano()), Format: "openai", Model: "bound-model", APIKey: "test-key", BaseURL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := s.m.pg.GetAgentByKey("worker")
	if err := s.m.pg.SetAgentLLMProfile("worker", &boundID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.m.pg.SetAgentLLMProfile("worker", worker.LLMProfileID)
		_ = s.m.pg.DeleteProfile(boundID)
	})
	runtime := &taskLLMRuntime{s: s, taskID: task.ID, agentKey: "worker"}
	cfg, ok := runtime.activeCfg()
	if !ok || cfg.Model != "demo/a" || cfg.ReasoningEffort != "high" || cfg.ThinkingType != "enabled" {
		t.Fatalf("default change/binding replaced saved choice: %+v", cfg)
	}
	selected, err := runtime.current()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := selected.provider.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{llm.UserText("test")}}); err != nil {
		t.Fatal(err)
	}
	if wire["model"] != "demo/a" || wire["reasoning_effort"] != "high" {
		t.Fatalf("wrong task wire request: %v", wire)
	}
	resolution, err := s.resolveTaskRoleLLM(task, "worker")
	if err != nil || !resolution.Available || resolution.Source != "task_chain" || resolution.Model != "demo/a" || resolution.ReasoningEffort != "high" {
		t.Fatalf("wrong displayed resolution: %+v %v", resolution, err)
	}
	_, next, err := s.resolveTaskModels(t.Context(), nil, taskModelRequest{})
	if err != nil || next[0].Model != "demo/b" || next[0].ReasoningEffort != "low" {
		t.Fatalf("new tasks did not pick updated default: %v %v", next, err)
	}
	num, _ := strconv.ParseInt(task.ID, 10, 64)
	reloaded, err := s.m.pg.GetTask(num)
	if err != nil || !reflect.DeepEqual(reloaded.LLMSelections, choices) {
		t.Fatalf("resume lost selection: %v %v", reloaded, err)
	}
	listed, err := s.m.pg.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range listed {
		if candidate.ID == num {
			found = reflect.DeepEqual(candidate.LLMSelections, choices)
		}
	}
	if !found {
		t.Fatal("bulk hydration lost selections")
	}

	callSpawn := func(args map[string]any) string {
		b, _ := json.Marshal(args)
		result, err := s.toolSpawnTask().Call(t.Context(), b, nil)
		if err != nil || result.IsError || len(result.Content) == 0 {
			t.Fatalf("spawn failed: %+v %v", result, err)
		}
		var output struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal([]byte(result.Content[0].Text), &output); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = s.m.DeleteTask(output.TaskID, DeleteTaskOptions{}) })
		return output.TaskID
	}
	for _, tc := range []struct {
		name                    string
		args                    map[string]any
		model, effort, thinking string
	}{
		{"explicit", map[string]any{"llm_profile_id": id, "llm_model": "demo/a", "reasoning_effort": "max"}, "demo/a", "max", ""},
		{"system", map[string]any{"llm_source": "system_default", "parent_ref": task.ID}, "demo/b", "low", "disabled"},
		{"inherit", map[string]any{"llm_source": "inherit_parent", "parent_ref": task.ID}, "demo/a", "high", "enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.args["description"], tc.args["goal"] = "model selection test", "test saved selection"
			childID := callSpawn(tc.args)
			childNum, _ := strconv.ParseInt(childID, 10, 64)
			child, _ := s.m.pg.GetTask(childNum)
			if len(child.LLMSelections) != 1 || child.LLMSelections[0].Model != tc.model || child.LLMSelections[0].ReasoningEffort != tc.effort || child.LLMSelections[0].ThinkingType != tc.thinking {
				t.Fatalf("spawn lost choice: %+v", child.LLMSelections)
			}
		})
	}
	for _, args := range []map[string]any{
		{"llm_profile_id": -1}, {"llm_profile_id": 999999999}, {"llm_profile_id": id, "llm_model": "missing"},
		{"llm_profile_id": id, "reasoning_effort": "invalid"}, {"llm_profile_id": id, "llm_source": "system_default"},
		{"llm_source": "inherit_parent"}, {"llm_source": "invalid"},
	} {
		args["goal"] = "invalid selection"
		b, _ := json.Marshal(args)
		result, err := s.toolSpawnTask().Call(t.Context(), b, nil)
		if err != nil || !result.IsError {
			t.Fatalf("accepted invalid spawn: %v -> %+v %v", args, result, err)
		}
	}
	result, err := s.toolListLLMModels().Call(t.Context(), json.RawMessage(fmt.Sprintf(`{"llm_profile_id":%d}`, id)), nil)
	if err != nil || result.IsError || !strings.Contains(result.Content[0].Text, "demo/a") || strings.Contains(result.Content[0].Text, p.APIKey) {
		t.Fatalf("unsafe/incomplete model catalog: %+v %v", result, err)
	}
	var schema string
	if err := s.m.pg.QueryRow(`SELECT schema::text FROM tools WHERE key='spawn_task'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "llm_model") || !strings.Contains(schema, "reasoning_effort") {
		t.Fatal("seeded task tool schema was not upgraded")
	}
}

func strPtr(v string) *string { return &v }

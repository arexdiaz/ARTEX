package db

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestTaskModelSelectionsSurviveFailoverReplacementAndArchive(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	var ids []int64
	for i := range 2 {
		id, err := d.SaveProfile(&LLMProfile{Name: fmt.Sprintf("snapshots-%d-%d", time.Now().UnixNano(), i), Format: "openai", DefaultModel: "default-model", APIKey: "test-key"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		t.Cleanup(func() { _ = d.DeleteProfile(id) })
	}
	choices := []TaskModelSelection{
		{ProfileID: ids[0], Model: "selected-a", ReasoningEffort: "high", ThinkingType: "enabled"},
		{ProfileID: ids[1], Model: "selected-b", ReasoningEffort: "", ThinkingType: "disabled"},
	}
	task, err := d.CreateTaskWithOptions("saved model failover", "persist task selections", TaskCreateOptions{LLMProfileIDs: ids, LLMSelections: choices})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	if _, err := d.MarkTaskLLMProfileQuotaExhausted(task.ID, ids[0], "insufficient credits"); err != nil {
		t.Fatal(err)
	}
	loaded, err := d.GetTask(task.ID)
	if err != nil || loaded.ActiveLLMProfileID == nil || *loaded.ActiveLLMProfileID != ids[1] || !reflect.DeepEqual(loaded.LLMSelections, choices) {
		t.Fatalf("failover changed selections: %+v %v", loaded, err)
	}
	if err := d.ReplaceTaskLLMProfiles(task.ID, ids, ids[0], choices); err != nil {
		t.Fatal(err)
	}
	loaded, _ = d.GetTask(task.ID)
	if !reflect.DeepEqual(loaded.LLMSelections, choices) {
		t.Fatal("replacement lost selections")
	}
	// The archive row shape preserves empty reasoning separately from absent
	// fields in archives of legacy tasks.
	rows := []map[string]any{}
	for i, choice := range choices {
		rows = append(rows, map[string]any{"task_id": task.ID, "profile_id": choice.ProfileID, "status": "ready", "position": i,
			"model": choice.Model, "reasoning_effort": choice.ReasoningEffort, "thinking_type": choice.ThinkingType})
	}
	raw, _ := json.Marshal(rows)
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM task_llm_profiles WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreTaskLLMProfiles(tx, raw); err != nil {
		t.Fatal(err)
	}
	for _, choice := range choices {
		var model, effort, thinking string
		if err := tx.QueryRow(`SELECT model,reasoning_effort,thinking_type FROM task_llm_profiles WHERE task_id=$1 AND profile_id=$2`, task.ID, choice.ProfileID).Scan(&model, &effort, &thinking); err != nil {
			t.Fatal(err)
		}
		if model != choice.Model || effort != choice.ReasoningEffort || thinking != choice.ThinkingType {
			t.Fatalf("archive lost choice: %s %s %s", model, effort, thinking)
		}
	}
}

package models_test

import (
	"encoding/json"
	"testing"

	"raspicd/internal/models"
)

func TestSigningMessageFormat(t *testing.T) {
	task := &models.Task{
		ID:     "tid123",
		Script: "deploy",
		Config: map[string]interface{}{"env": "prod"},
	}
	got, err := task.SigningMessage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfgJSON, _ := json.Marshal(task.Config)
	want := "tid123|deploy|" + string(cfgJSON)
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSigningMessageNilConfig(t *testing.T) {
	task := &models.Task{ID: "a", Script: "b"}
	got, err := task.SigningMessage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "a|b|null" {
		t.Errorf("got %q, want \"a|b|null\"", got)
	}
}

func TestSigningMessageEmptyConfig(t *testing.T) {
	task := &models.Task{ID: "x", Script: "y", Config: map[string]interface{}{}}
	got, err := task.SigningMessage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "x|y|{}" {
		t.Errorf("got %q, want \"x|y|{}\"", got)
	}
}

func TestSigningMessageDeterministic(t *testing.T) {
	task := &models.Task{
		ID:     "x",
		Script: "y",
		Config: map[string]interface{}{"a": 1.0, "b": "two"},
	}
	m1, err := task.SigningMessage()
	if err != nil {
		t.Fatal(err)
	}
	m2, err := task.SigningMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(m1) != string(m2) {
		t.Error("signing message is not deterministic across calls")
	}
}

func TestSigningMessageCoversID(t *testing.T) {
	t1 := &models.Task{ID: "id1", Script: "s"}
	t2 := &models.Task{ID: "id2", Script: "s"}
	m1, _ := t1.SigningMessage()
	m2, _ := t2.SigningMessage()
	if string(m1) == string(m2) {
		t.Error("different IDs must produce different signing messages")
	}
}

func TestSigningMessageCoversScript(t *testing.T) {
	t1 := &models.Task{ID: "id", Script: "deploy"}
	t2 := &models.Task{ID: "id", Script: "restart"}
	m1, _ := t1.SigningMessage()
	m2, _ := t2.SigningMessage()
	if string(m1) == string(m2) {
		t.Error("different scripts must produce different signing messages")
	}
}

func TestSigningMessageCoversConfig(t *testing.T) {
	t1 := &models.Task{ID: "id", Script: "s", Config: map[string]interface{}{"k": "v1"}}
	t2 := &models.Task{ID: "id", Script: "s", Config: map[string]interface{}{"k": "v2"}}
	m1, _ := t1.SigningMessage()
	m2, _ := t2.SigningMessage()
	if string(m1) == string(m2) {
		t.Error("different configs must produce different signing messages")
	}
}

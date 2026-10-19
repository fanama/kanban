package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCardUnmarshalAcceptsStringConstraints(t *testing.T) {
	var card Card
	if err := json.Unmarshal([]byte(`{"id":"c","title":"Card","depends_on":"a, b;c"}`), &card); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !reflect.DeepEqual(card.DependsOn, []string{"a", "b", "c"}) {
		t.Fatalf("depends_on = %v, want [a b c]", card.DependsOn)
	}
}

func TestCardUnmarshalAcceptsArrayConstraints(t *testing.T) {
	var card Card
	if err := json.Unmarshal([]byte(`{"id":"c","title":"Card","depends_on":["a","b"]}`), &card); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !reflect.DeepEqual(card.DependsOn, []string{"a", "b"}) {
		t.Fatalf("depends_on = %v, want [a b]", card.DependsOn)
	}
}

func TestCardUnmarshalRejectsOtherConstraints(t *testing.T) {
	var card Card
	if err := json.Unmarshal([]byte(`{"id":"c","title":"Card","depends_on":3}`), &card); err == nil {
		t.Fatal("Expected an error for a numeric depends_on")
	}
}

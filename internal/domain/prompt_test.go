package domain

import (
	"reflect"
	"testing"
)

func TestSortPrompts_DeterministicOrdering(t *testing.T) {
	p1 := Prompt{Number: 1, Title: "One"}
	p2 := Prompt{Number: 2, Title: "Two"}
	p3 := Prompt{Number: 3, Title: "Three"}
	p4 := Prompt{Number: 4, Title: "Four"}
	p5 := Prompt{Number: 5, Title: "Five"}

	expected := []Prompt{p1, p2, p3, p4, p5}

	shuffles := [][]Prompt{
		{p5, p2, p4, p1, p3},
		{p3, p1, p5, p2, p4},
		{p5, p4, p3, p2, p1},
		{p1, p2, p3, p4, p5},
		{p2, p5, p1, p3, p4},
	}

	for i, shuffle := range shuffles {
		sorted := SortPrompts(shuffle)
		if !reflect.DeepEqual(sorted, expected) {
			t.Errorf("shuffle %d: got numbers %v, want %v", i, promptNumbers(sorted), promptNumbers(expected))
		}
	}
}

func TestSortPrompts_EdgeCases(t *testing.T) {
	if len(SortPrompts(nil)) != 0 {
		t.Error("nil slice should return empty")
	}
	single := []Prompt{{Number: 42}}
	sortedSingle := SortPrompts(single)
	if len(sortedSingle) != 1 || sortedSingle[0].Number != 42 {
		t.Errorf("single element sort failed: %v", sortedSingle)
	}
}

func promptNumbers(prompts []Prompt) []PromptNumber {
	nums := make([]PromptNumber, len(prompts))
	for i, p := range prompts {
		nums[i] = p.Number
	}
	return nums
}

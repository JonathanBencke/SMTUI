package logbuffer

import (
	"reflect"
	"testing"
)

func TestLinesSince_ReturnsOnlyNewLines(t *testing.T) {
	rb := New(10)
	rb.Write("a")
	rb.Write("b")

	lines, next := rb.LinesSince(0)
	if !reflect.DeepEqual(lines, []string{"a", "b"}) {
		t.Fatalf("LinesSince(0) = %v, want [a b]", lines)
	}
	if next != 2 {
		t.Fatalf("next = %d, want 2", next)
	}

	rb.Write("c")

	lines, next = rb.LinesSince(next)
	if !reflect.DeepEqual(lines, []string{"c"}) {
		t.Errorf("LinesSince(2) = %v, want [c]", lines)
	}
	if next != 3 {
		t.Errorf("next = %d, want 3", next)
	}
}

func TestLinesSince_ClampsEvictedIndexes(t *testing.T) {
	rb := New(2)
	rb.Write("a")
	rb.Write("b")
	rb.Write("c")

	// "a" was evicted; asking from 0 must return what is still retained rather
	// than fail or return stale data.
	lines, next := rb.LinesSince(0)
	if !reflect.DeepEqual(lines, []string{"b", "c"}) {
		t.Errorf("LinesSince(0) = %v, want [b c]", lines)
	}
	if next != 3 {
		t.Errorf("next = %d, want 3", next)
	}
}

func TestLinesSince_AtCursorReturnsNothing(t *testing.T) {
	rb := New(4)
	rb.Write("a")

	lines, next := rb.LinesSince(rb.Written())
	if len(lines) != 0 {
		t.Errorf("LinesSince(cursor) = %v, want no lines", lines)
	}
	if next != 1 {
		t.Errorf("next = %d, want 1", next)
	}
}

// TestClear_KeepsCursorMonotonic guards the cursor contract: after a clear, a
// reader holding an old index must not be handed lines it already consumed.
func TestClear_KeepsCursorMonotonic(t *testing.T) {
	rb := New(4)
	rb.Write("a")
	rb.Write("b")
	rb.Clear()

	if got := rb.Written(); got != 2 {
		t.Fatalf("Written() = %d, want 2 (the cursor never rewinds)", got)
	}

	lines, next := rb.LinesSince(0)
	if len(lines) != 0 {
		t.Errorf("LinesSince(0) = %v, want no lines after Clear", lines)
	}
	if next != 2 {
		t.Errorf("next = %d, want 2", next)
	}

	rb.Write("c")
	lines, _ = rb.LinesSince(next)
	if !reflect.DeepEqual(lines, []string{"c"}) {
		t.Errorf("LinesSince(2) = %v, want [c]", lines)
	}
}

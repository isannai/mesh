package queue

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestJobChunks(t *testing.T) {
	j := &Job{ID: "abc"}
	if j.ChunkCount() != 0 {
		t.Fatalf("empty ChunkCount = %d, want 0", j.ChunkCount())
	}
	if _, ok := j.ChunkAt(0); ok {
		t.Fatalf("ChunkAt(0) on empty should be !ok")
	}

	j.AppendChunk("첫 문장입니다.")
	j.AppendChunk("Second one.")
	if j.ChunkCount() != 2 {
		t.Fatalf("ChunkCount = %d, want 2", j.ChunkCount())
	}
	if s, ok := j.ChunkAt(0); !ok || s != "첫 문장입니다." {
		t.Fatalf("ChunkAt(0) = %q,%v", s, ok)
	}
	if s, ok := j.ChunkAt(1); !ok || s != "Second one." {
		t.Fatalf("ChunkAt(1) = %q,%v", s, ok)
	}
	if _, ok := j.ChunkAt(2); ok {
		t.Fatalf("ChunkAt(2) should be out of range")
	}
	if _, ok := j.ChunkAt(-1); ok {
		t.Fatalf("ChunkAt(-1) should be !ok")
	}
}

func TestJobMarshalChunkCount(t *testing.T) {
	// Non-streaming job → chunk_count entirely absent (unchanged wire).
	j := &Job{ID: "x", Status: StatusRunning}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "chunk_count") {
		t.Fatalf("non-stream job should omit chunk_count: %s", b)
	}
	if !strings.Contains(string(b), `"job_id":"x"`) || !strings.Contains(string(b), `"status":"running"`) {
		t.Fatalf("core fields missing: %s", b)
	}

	// Streaming job → chunk_count always present, even at 0 (so a poller sees
	// it from the first tick).
	sj := &Job{ID: "y", Stream: true, Status: StatusRunning}
	b, _ = json.Marshal(sj)
	if !strings.Contains(string(b), `"chunk_count":0`) {
		t.Fatalf("stream job should show chunk_count:0, got %s", b)
	}
	sj.AppendChunk("a.")
	sj.AppendChunk("b.")
	sj.AppendChunk("c.")
	b, _ = json.Marshal(sj)
	if !strings.Contains(string(b), `"chunk_count":3`) {
		t.Fatalf("want chunk_count:3, got %s", b)
	}
}

// TestJobChunkConcurrency exercises AppendChunk vs ChunkCount/ChunkAt/Marshal
// under -race: a writer appends while readers poll, mirroring the worker
// streaming while status/chunk requests read.
func TestJobChunkConcurrency(t *testing.T) {
	j := &Job{ID: "race"}
	const n = 200
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			j.AppendChunk("s")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = j.ChunkCount()
			_, _ = j.ChunkAt(i % 10)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_, _ = json.Marshal(j)
		}
	}()
	wg.Wait()
	if j.ChunkCount() != n {
		t.Fatalf("final ChunkCount = %d, want %d", j.ChunkCount(), n)
	}
}

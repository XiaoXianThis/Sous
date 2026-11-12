package brain

import (
	"bytes"
	"math/rand"
	"testing"
)

// An insertion near the start moves every later byte; content-defined
// chunks must still match after it, so only the chunks around it upload.
func TestCDCSurvivesInsertion(t *testing.T) {
	data := make([]byte, 8<<20)
	rand.New(rand.NewSource(1)).Read(data)
	before := cdcSplit(data)
	if !bytes.Equal(bytes.Join(before, nil), data) {
		t.Fatal("chunks do not reassemble")
	}
	for _, c := range before[:len(before)-1] {
		if len(c) < cdcMin || len(c) > cdcMax {
			t.Fatalf("chunk of %d bytes", len(c))
		}
	}
	edited := append(append(append([]byte{}, data[:100<<10]...), []byte("a new row")...), data[100<<10:]...)
	after := cdcSplit(edited)
	have := map[string]bool{}
	key := bytes.Repeat([]byte{7}, 32)
	for _, c := range before {
		have[chunkName(key, c)] = true
	}
	changed := 0
	for _, c := range after {
		if !have[chunkName(key, c)] {
			changed++
		}
	}
	if changed > 2 {
		t.Fatalf("%d of %d chunks changed after one insertion", changed, len(after))
	}
}

func TestChunkSealRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	chunk := bytes.Repeat([]byte("hub.db page "), 5000)
	name := chunkName(key, chunk)
	ct, err := sealChunk(key, name, chunk)
	if err != nil {
		t.Fatal(err)
	}
	if len(ct) >= len(chunk)/2 {
		t.Fatalf("chunk not compressed: %d of %d", len(ct), len(chunk))
	}
	got, err := openChunk(key, name, ct)
	if err != nil || !bytes.Equal(got, chunk) {
		t.Fatal("round trip:", err)
	}
	if _, err := openChunk(key, chunkName(key, []byte("other")), ct); err == nil {
		t.Fatal("a chunk opened under another name")
	}
}

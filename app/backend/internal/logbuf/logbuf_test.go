package logbuf

import (
	"io"
	"log/slog"
	"testing"
)

func TestKeepsWarningsAndErrorsNewestFirst(t *testing.T) {
	reset()
	log := slog.New(Wrap(slog.NewTextHandler(io.Discard, nil))).With("component", "test")
	log.Info("ignored")
	log.Warn("first", "n", 1)
	log.WithGroup("pg").Error("second", "err", "boom")
	got := Recent(10)
	if len(got) != 2 || got[0].Message != "second" || got[1].Message != "first" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Attrs["pg.err"] != "boom" || got[0].Attrs["component"] != "test" || got[1].Attrs["n"] != "1" {
		t.Fatalf("attrs %+v", got)
	}
	if c := Counts(); c["warn"] != 1 || c["error"] != 1 {
		t.Fatalf("counts %v", c)
	}
}

func TestRingWrapsAround(t *testing.T) {
	reset()
	log := slog.New(Wrap(slog.NewTextHandler(io.Discard, nil)))
	for i := range size + 5 {
		log.Error("e", "i", i)
	}
	got := Recent(size * 2)
	if len(got) != size || got[0].Attrs["i"] != "104" || got[size-1].Attrs["i"] != "5" {
		t.Fatalf("len %d first %v last %v", len(got), got[0].Attrs, got[size-1].Attrs)
	}
	if Counts()["error"] != size+5 {
		t.Fatal(Counts())
	}
}

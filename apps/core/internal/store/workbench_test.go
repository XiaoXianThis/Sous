package store_test

import (
	"context"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func put(t *testing.T, s *store.Store, evs ...event.Event) {
	t.Helper()
	if _, _, err := s.Write(context.Background(), func(w *store.Writer) error {
		for _, ev := range evs {
			if _, err := w.Append(ev); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScansLinkProjectsByRemote(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	put(t, s,
		event.New(event.NodePaired, event.ActorSystem, "", event.NodePayload{NodeID: "n1", Name: "mac", OS: "darwin", Arch: "arm64"}),
		event.New(event.ProjectPut, event.ActorUser, "", event.ProjectPayload{ProjectID: "p1", Name: "sous", RepoURL: "https://github.com/x/Sous.git",
			Nodes: []event.ProjectNode{{NodeID: "n9", Path: "/manual", Priority: 0}}}),
		event.New(event.NodeRepos, event.ActorSystem, "", event.NodeReposPayload{NodeID: "n1", NodeRepos: proto.NodeRepos{
			Workspaces: []string{"/code"},
			Repos: []proto.Repo{
				{Path: "/code/sous", Remote: "git@github.com:x/Sous.git", Branch: "main", Wokspace: true},
				{Path: "/code/other", Remote: "https://github.com/x/other"},
			},
		}}),
	)
	p, err := s.Project(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 2 || p.Nodes[1].NodeID != "n1" || p.Nodes[1].Path != "/code/sous" || p.Nodes[1].Source != "scan" {
		t.Fatalf("nodes = %+v", p.Nodes)
	}
	if p.Layout != "wokspace" || p.RemoteKey != "github.com/x/Sous" {
		t.Fatalf("layout %q key %q", p.Layout, p.RemoteKey)
	}
	scans, _ := s.RepoScans(ctx)
	if len(scans) != 2 || scans[1].ProjectID != "p1" || scans[0].ProjectID != "" {
		t.Fatalf("scans = %+v", scans)
	}
	n, _ := s.Node(ctx, "n1")
	if len(n.Workspaces) != 1 || n.Role != "dev" {
		t.Fatalf("node = %+v", n)
	}

	// Editing the project keeps the scanned place; the repository going away removes it.
	put(t, s, event.New(event.ProjectPut, event.ActorUser, "", event.ProjectPayload{ProjectID: "p1", Name: "sous", RepoURL: "https://github.com/x/Sous"}))
	if p, _ = s.Project(ctx, "p1"); len(p.Nodes) != 1 || p.Nodes[0].Source != "scan" {
		t.Fatalf("after edit: %+v", p.Nodes)
	}
	put(t, s, event.New(event.NodeRepos, event.ActorSystem, "", event.NodeReposPayload{NodeID: "n1", NodeRepos: proto.NodeRepos{Workspaces: []string{"/code"}}}))
	if p, _ = s.Project(ctx, "p1"); len(p.Nodes) != 0 {
		t.Fatalf("after removal: %+v", p.Nodes)
	}
}

func TestPersonas(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	put(t, s, event.New(event.PersonaPut, event.ActorUser, "", event.PersonaPayload{PersonaID: "be", Name: "后端", Glyph: "后", Agent: "codex",
		Options: map[string]string{"model": "gpt-5"}, Kinds: []string{"code"}}))
	p, err := s.Persona(ctx, "be")
	if err != nil || p.Options["model"] != "gpt-5" || p.Kinds[0] != "code" {
		t.Fatalf("%+v %v", p, err)
	}
	put(t, s, event.New(event.PersonaDeleted, event.ActorUser, "", event.PersonaPayload{PersonaID: "be"}))
	if _, err := s.Persona(ctx, "be"); err != store.ErrNotFound {
		t.Fatal(err)
	}
}

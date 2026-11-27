package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dhanesh/phronesis/internal/audit"
	"github.com/dhanesh/phronesis/internal/mcp"
	"github.com/dhanesh/phronesis/internal/principal"
	"github.com/dhanesh/phronesis/internal/wiki"
)

// Wiki-aware MCP tools (beta scope, decision 2026-11-27): list, read, write
// and search pages. Each call is authorised like the HTTP API: the
// principal's role must allow the action (read/write), and a workspace-
// pinned credential may only touch its own workspace.

type wikiTool struct {
	name, desc string
	schema     string
	call       func(ctx context.Context, p principal.Principal, raw json.RawMessage) (any, error)
}

func (t *wikiTool) Name() string                 { return t.name }
func (t *wikiTool) Description() string          { return t.desc }
func (t *wikiTool) InputSchema() json.RawMessage { return json.RawMessage(t.schema) }
func (t *wikiTool) Call(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := principal.FromContext(ctx)
	if err != nil {
		return nil, errors.New("authentication required")
	}
	return t.call(ctx, p, raw)
}

var errMCPForbidden = errors.New("forbidden")

func decodeArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", mcp.ErrInvalidParams, err)
	}
	return nil
}

// mcpWorkspace authorises action on the workspace (default when empty).
func (s *Server) mcpWorkspace(p principal.Principal, action, slug string) (*wiki.Store, *wiki.Hub, string, error) {
	if slug == "" {
		slug = p.WorkspaceID
	}
	if slug == "" {
		slug = wiki.DefaultWorkspaceSlug
	}
	if !p.Can(action) {
		return nil, nil, "", fmt.Errorf("%w: role %s cannot %s pages", errMCPForbidden, p.Role, action)
	}
	if p.WorkspaceID != "" && p.WorkspaceID != slug && p.IsServiceAccount() {
		return nil, nil, "", fmt.Errorf("%w: credential is not valid for workspace %s", errMCPForbidden, slug)
	}
	store, hub, ok := s.workspaces.Get(slug)
	if !ok {
		return nil, nil, "", fmt.Errorf("%w: unknown workspace %q", mcp.ErrInvalidParams, slug)
	}
	return store, hub, slug, nil
}

const wsProp = `"workspace": {"type": "string", "description": "Workspace slug; defaults to the credential's workspace."}`

func (s *Server) wikiMCPTools() []mcp.Tool {
	return []mcp.Tool{
		&wikiTool{
			name:   "list_pages",
			desc:   "List the wiki pages in a workspace (name and last update).",
			schema: `{"type":"object","properties":{` + wsProp + `},"additionalProperties":false}`,
			call: func(ctx context.Context, p principal.Principal, raw json.RawMessage) (any, error) {
				var a struct {
					Workspace string `json:"workspace"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				store, _, slug, err := s.mcpWorkspace(p, "read", a.Workspace)
				if err != nil {
					return nil, err
				}
				pages, err := store.List()
				if err != nil {
					return nil, err
				}
				if pages == nil {
					pages = []wiki.Summary{}
				}
				return map[string]any{"workspace": slug, "pages": pages}, nil
			},
		},
		&wikiTool{
			name: "read_page",
			desc: "Read a wiki page's Markdown, version, links, tags and backlinks. A missing page returns empty content and version 0.",
			schema: `{"type":"object","properties":{"name":{"type":"string","description":"Page name, e.g. roadmap or team/notes"},` +
				wsProp + `},"required":["name"],"additionalProperties":false}`,
			call: func(ctx context.Context, p principal.Principal, raw json.RawMessage) (any, error) {
				var a struct{ Name, Workspace string }
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				if strings.TrimSpace(a.Name) == "" {
					return nil, fmt.Errorf("%w: name is required", mcp.ErrInvalidParams)
				}
				_, hub, slug, err := s.mcpWorkspace(p, "read", a.Workspace)
				if err != nil {
					return nil, err
				}
				page, err := hub.Snapshot(a.Name)
				if err != nil {
					return nil, err
				}
				s.recordMCPAudit(ctx, p, "doc.view", page.Name, slug)
				return map[string]any{
					"workspace": slug, "name": page.Name, "content": page.Content, "version": page.Version,
					"links": page.Render.Links, "tags": page.Render.Tags, "backlinks": page.Render.Backlinks,
				}, nil
			},
		},
		&wikiTool{
			name: "write_page",
			desc: "Create or replace a wiki page's Markdown. Pass base_version from read_page to merge with concurrent edits; omit it to overwrite the latest version.",
			schema: `{"type":"object","properties":{"name":{"type":"string"},"content":{"type":"string","description":"Full Markdown content"},` +
				`"base_version":{"type":"integer","description":"Version the edit was based on (from read_page)"},` + wsProp +
				`},"required":["name","content"],"additionalProperties":false}`,
			call: func(ctx context.Context, p principal.Principal, raw json.RawMessage) (any, error) {
				var a struct {
					Name        string `json:"name"`
					Content     string `json:"content"`
					BaseVersion *int64 `json:"base_version"`
					Workspace   string `json:"workspace"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				if strings.TrimSpace(a.Name) == "" {
					return nil, fmt.Errorf("%w: name is required", mcp.ErrInvalidParams)
				}
				_, hub, slug, err := s.mcpWorkspace(p, "write", a.Workspace)
				if err != nil {
					return nil, err
				}
				base := int64(0)
				if a.BaseVersion != nil {
					base = *a.BaseVersion
				} else {
					cur, err := hub.Snapshot(a.Name)
					if err != nil {
						return nil, err
					}
					base = cur.Version
				}
				page, merged, err := hub.Apply(a.Name, base, a.Content, p.ID)
				if err != nil {
					return nil, err
				}
				s.recordMCPAudit(ctx, p, "doc.edit", page.Name, slug)
				return map[string]any{"workspace": slug, "name": page.Name, "version": page.Version, "merged": merged}, nil
			},
		},
		&wikiTool{
			name: "search_pages",
			desc: "Find pages whose name or content contains the query (case-insensitive). Returns up to limit matches with a snippet.",
			schema: `{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":50},` +
				wsProp + `},"required":["query"],"additionalProperties":false}`,
			call: func(ctx context.Context, p principal.Principal, raw json.RawMessage) (any, error) {
				var a struct {
					Query     string `json:"query"`
					Limit     int    `json:"limit"`
					Workspace string `json:"workspace"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				q := strings.ToLower(strings.TrimSpace(a.Query))
				if q == "" {
					return nil, fmt.Errorf("%w: query is required", mcp.ErrInvalidParams)
				}
				if a.Limit <= 0 || a.Limit > 50 {
					a.Limit = 20
				}
				store, _, slug, err := s.mcpWorkspace(p, "read", a.Workspace)
				if err != nil {
					return nil, err
				}
				return searchPages(store, slug, q, a.Limit)
			},
		},
	}
}

type searchHit struct {
	Name    string `json:"name"`
	Snippet string `json:"snippet"`
}

func searchPages(store *wiki.Store, slug, q string, limit int) (any, error) {
	pages, err := store.List()
	if err != nil {
		return nil, err
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].UpdatedAt.After(pages[j].UpdatedAt) })
	hits := []searchHit{}
	for _, sum := range pages {
		if len(hits) >= limit {
			break
		}
		page, err := store.Get(sum.Name)
		if err != nil {
			continue
		}
		lc := strings.ToLower(page.Content)
		idx := strings.Index(lc, q)
		if idx < 0 && !strings.Contains(strings.ToLower(page.Name), q) {
			continue
		}
		snippet := ""
		if idx >= 0 {
			start, end := max(0, idx-60), min(len(page.Content), idx+len(q)+60)
			snippet = strings.TrimSpace(page.Content[start:end])
		}
		hits = append(hits, searchHit{Name: page.Name, Snippet: snippet})
	}
	return map[string]any{"workspace": slug, "query": q, "results": hits}, nil
}

func (s *Server) recordMCPAudit(_ context.Context, p principal.Principal, action, page, slug string) {
	s.recordAudit(audit.Event{
		At:            time.Now().UTC(),
		Action:        action,
		PrincipalID:   p.ID,
		PrincipalType: string(p.Type),
		WorkspaceID:   slug,
		ResourceID:    page,
		Metadata:      map[string]string{"via": "mcp"},
	})
}

// Package builtinrulegroups embeds a catalog of platform-provided rule groups
// (currently the 1C starter set adapted from github.com/comol/ai_rules_1c) and
// seeds them as real rows into each workspace so they show up in the UI, can be
// bound to projects/squads/agents, and flow through the same effective-rules
// assembly as user-authored groups.
//
// Layout: catalog/<group-slug>/group.md describes the group (frontmatter:
// name, description, version) and catalog/<group-slug>/NNN-<rule>.md describes
// one rule each (frontmatter: name, description, sort_order, file_name; the
// markdown body is the rule content). The numeric filename prefix only orders
// files on disk; sort_order in frontmatter is the authoritative ordering.
package builtinrulegroups

import (
	"embed"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed catalog
var catalogFS embed.FS

const catalogRoot = "catalog"

// Group is a parsed builtin rule group from the embedded catalog.
type Group struct {
	// Slug is the on-disk directory name (e.g. "1c-core"); it is not persisted
	// but is handy for logging and stable ordering.
	Slug        string
	Name        string
	Description string
	Version     string
	Rules       []Rule
}

// Rule is a single markdown rule inside a group.
type Rule struct {
	Name        string
	Description string
	Content     string
	SortOrder   int32
	FileName    string
}

// Keeps the trailing newline of the YAML block in group 1, matching the
// behaviour relied on by the shared skill frontmatter parser.
var frontmatterPattern = regexp.MustCompile(`(?s)\A---\r?\n(.*?\r?\n)---\r?\n?`)

// Catalog returns every builtin rule group parsed from the embedded catalog,
// ordered by slug for deterministic seeding. Malformed groups (missing
// group.md, no usable rules) are skipped rather than failing the whole load.
func Catalog() []Group {
	entries, err := fs.ReadDir(catalogFS, catalogRoot)
	if err != nil {
		return nil
	}
	var groups []Group
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if g, ok := loadGroup(entry.Name()); ok {
			groups = append(groups, g)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Slug < groups[j].Slug })
	return groups
}

func loadGroup(slug string) (Group, bool) {
	dir := path.Join(catalogRoot, slug)
	groupMeta, ok := readMarkdown(path.Join(dir, "group.md"))
	if !ok || strings.TrimSpace(groupMeta.fields["name"]) == "" {
		// A group directory without a named group.md is malformed.
		return Group{}, false
	}
	g := Group{
		Slug:        slug,
		Name:        strings.TrimSpace(groupMeta.fields["name"]),
		Description: strings.TrimSpace(groupMeta.fields["description"]),
		Version:     strings.TrimSpace(groupMeta.fields["version"]),
	}

	entries, err := fs.ReadDir(catalogFS, dir)
	if err != nil {
		return Group{}, false
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "group.md" || !strings.HasSuffix(name, ".md") {
			continue
		}
		md, ok := readMarkdown(path.Join(dir, name))
		if !ok {
			continue
		}
		ruleName := strings.TrimSpace(md.fields["name"])
		content := strings.TrimSpace(md.body)
		if ruleName == "" || content == "" {
			// The DB enforces non-empty name and content; skip anything that
			// would be rejected so seeding never errors on a bad file.
			continue
		}
		g.Rules = append(g.Rules, Rule{
			Name:        ruleName,
			Description: strings.TrimSpace(md.fields["description"]),
			Content:     content,
			SortOrder:   parseSortOrder(md.fields["sort_order"]),
			FileName:    strings.TrimSpace(md.fields["file_name"]),
		})
	}
	if len(g.Rules) == 0 {
		return Group{}, false
	}
	sort.SliceStable(g.Rules, func(i, j int) bool {
		if g.Rules[i].SortOrder != g.Rules[j].SortOrder {
			return g.Rules[i].SortOrder < g.Rules[j].SortOrder
		}
		return g.Rules[i].Name < g.Rules[j].Name
	})
	return g, true
}

type markdown struct {
	fields map[string]string
	body   string
}

func readMarkdown(p string) (markdown, bool) {
	raw, err := fs.ReadFile(catalogFS, p)
	if err != nil {
		return markdown{}, false
	}
	content := string(raw)
	match := frontmatterPattern.FindStringSubmatchIndex(content)
	if match == nil {
		return markdown{fields: map[string]string{}, body: content}, true
	}
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(content[match[2]:match[3]]), &fm); err != nil {
		return markdown{}, false
	}
	fields := make(map[string]string, len(fm))
	for k, v := range fm {
		fields[k] = coerceScalar(v)
	}
	return markdown{fields: fields, body: content[match[1]:]}, true
}

// coerceScalar renders a decoded YAML scalar as a string. Frontmatter values in
// this catalog are always scalars (name, description, version, sort_order,
// file_name), so non-scalar values are intentionally rendered empty.
func coerceScalar(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case int:
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatInt(int64(val), 10)
	case bool:
		return strconv.FormatBool(val)
	default:
		return ""
	}
}

func parseSortOrder(s string) int32 {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return int32(n)
}

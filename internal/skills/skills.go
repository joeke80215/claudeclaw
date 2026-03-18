// Package skills discovers and resolves Claude Code skills from project, global,
// and plugin directories.
package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SkillInfo holds the name and description of a discovered skill.
type SkillInfo struct {
	Name        string
	Description string
}

// ListSkills scans project, global, and plugin skill directories and returns
// all available skills. Skills found earlier in the search order take precedence
// (project > global > plugins).
func ListSkills() []SkillInfo {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	projectSkillsDir := filepath.Join(cwd, ".claude", "skills")
	globalSkillsDir := filepath.Join(home, ".claude", "skills")
	pluginsDir := filepath.Join(home, ".claude", "plugins")

	seen := make(map[string]bool)
	var skills []SkillInfo

	collectSkillsFromDir(projectSkillsDir, "", seen, &skills)
	collectSkillsFromDir(globalSkillsDir, "", seen, &skills)

	cachePath := filepath.Join(pluginsDir, "cache")
	if info, err := os.Stat(cachePath); err == nil && info.IsDir() {
		pluginDirs, err := os.ReadDir(cachePath)
		if err == nil {
			for _, pd := range pluginDirs {
				if !pd.IsDir() {
					continue
				}
				pluginCacheDir := filepath.Join(cachePath, pd.Name())
				subDirs, err := os.ReadDir(pluginCacheDir)
				if err != nil {
					continue
				}
				for _, sub := range subDirs {
					if !sub.IsDir() {
						continue
					}
					innerDir := filepath.Join(pluginCacheDir, sub.Name())
					// Look for versioned dirs inside.
					verDirs, err := os.ReadDir(innerDir)
					if err == nil {
						for _, ver := range verDirs {
							if !ver.IsDir() {
								continue
							}
							collectSkillsFromDir(
								filepath.Join(innerDir, ver.Name(), "skills"),
								pd.Name(), seen, &skills)
						}
					}
					// Also check directly (non-versioned).
					collectSkillsFromDir(
						filepath.Join(innerDir, "skills"),
						pd.Name(), seen, &skills)
				}
			}
		}
	}

	return skills
}

// collectSkillsFromDir scans a directory for skill subdirectories containing SKILL.md.
func collectSkillsFromDir(dir, pluginName string, seen map[string]bool, skills *[]SkillInfo) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		content := string(data)
		if strings.TrimSpace(content) == "" {
			continue
		}

		name := entry.Name()
		if pluginName != "" {
			name = pluginName + "_" + entry.Name()
		}
		if seen[name] {
			continue
		}
		seen[name] = true

		*skills = append(*skills, SkillInfo{
			Name:        name,
			Description: extractDescription(content),
		})
	}
}

var (
	frontmatterRe   = regexp.MustCompile(`(?s)^---\n(.*?)\n---`)
	descMultilineRe = regexp.MustCompile(`(?m)^description:\s*>?\s*\n?([\s\S]*?)(?:\n\w|\n---|\n$)`)
	descSingleRe    = regexp.MustCompile(`(?m)^description:\s*["']?(.+?)["']?\s*$`)
)

// extractDescription pulls a short description from SKILL.md content.
// It first checks YAML frontmatter for a description field, then falls back
// to the first non-empty, non-heading line.
func extractDescription(content string) string {
	fmMatch := frontmatterRe.FindStringSubmatch(content)
	if fmMatch != nil {
		fm := fmMatch[1]

		// Try multiline description.
		descMatch := descMultilineRe.FindStringSubmatch(fm)
		if descMatch != nil {
			raw := strings.TrimSpace(strings.ReplaceAll(descMatch[1], "\n", " "))
			// Collapse runs of whitespace.
			raw = collapseSpaces(raw)
			if raw != "" {
				return truncate(raw, 256)
			}
		}

		// Try single-line description.
		singleMatch := descSingleRe.FindStringSubmatch(fm)
		if singleMatch != nil {
			return truncate(strings.TrimSpace(singleMatch[1]), 256)
		}
	}

	// Fall back to first non-empty, non-heading, non-frontmatter line.
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		return truncate(trimmed, 256)
	}
	return "Claude Code skill"
}

// ResolveSkillPrompt resolves a slash command name to the contents of its SKILL.md.
// Search order:
//  1. Project skills: {cwd}/.claude/skills/{name}/SKILL.md
//  2. Global skills: ~/.claude/skills/{name}/SKILL.md
//  3. Plugin skills: ~/.claude/plugins/*/skills/{name}/SKILL.md and cache dirs
//
// The "plugin:skill" format restricts the search to a specific plugin.
// Returns the SKILL.md content if found, or an error if not found.
func ResolveSkillPrompt(command string) (string, error) {
	// Strip leading "/" if present.
	name := command
	if strings.HasPrefix(name, "/") {
		name = name[1:]
	}
	if name == "" {
		return "", os.ErrNotExist
	}

	// Handle "plugin:skill" format.
	var pluginHint, skillName string
	if idx := strings.Index(name, ":"); idx > 0 {
		pluginHint = name[:idx]
		skillName = name[idx+1:]
	} else {
		skillName = name
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	projectSkillsDir := filepath.Join(cwd, ".claude", "skills")
	globalSkillsDir := filepath.Join(home, ".claude", "skills")
	pluginsDir := filepath.Join(home, ".claude", "plugins")

	// 1. Project-level skills (exact name match).
	if pluginHint == "" {
		if content, err := tryReadSkillFile(filepath.Join(projectSkillsDir, skillName, "SKILL.md")); err == nil {
			return content, nil
		}
	}

	// 2. Global skills (exact name match).
	if pluginHint == "" {
		if content, err := tryReadSkillFile(filepath.Join(globalSkillsDir, skillName, "SKILL.md")); err == nil {
			return content, nil
		}
	}

	// 3. Plugin skills.
	if content, err := searchPluginSkills(pluginsDir, skillName, pluginHint); err == nil {
		return content, nil
	}

	return "", os.ErrNotExist
}

// tryReadSkillFile reads and returns the trimmed content of a file, or an error
// if the file doesn't exist or is empty.
func tryReadSkillFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", os.ErrNotExist
	}
	return content, nil
}

// searchPluginSkills searches for a skill in plugin directories.
func searchPluginSkills(pluginsDir, skillName, pluginHint string) (string, error) {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// If pluginHint is given, only check that specific plugin.
		if pluginHint != "" && entry.Name() != pluginHint {
			continue
		}

		// Direct plugin skills directory.
		skillPath := filepath.Join(pluginsDir, entry.Name(), "skills", skillName, "SKILL.md")
		if content, err := tryReadSkillFile(skillPath); err == nil {
			return content, nil
		}

		// Also check cache dir structure.
		cachePath := filepath.Join(pluginsDir, "cache", entry.Name())
		if content, err := searchCacheDir(cachePath, skillName); err == nil {
			return content, nil
		}
	}

	// Direct cache search when no plugin dirs matched.
	if pluginHint == "" {
		cachePath := filepath.Join(pluginsDir, "cache")
		cacheEntries, err := os.ReadDir(cachePath)
		if err == nil {
			for _, ce := range cacheEntries {
				if !ce.IsDir() {
					continue
				}
				if content, err := searchCacheDir(filepath.Join(cachePath, ce.Name()), skillName); err == nil {
					return content, nil
				}
			}
		}
	} else {
		// Direct cache search for specific plugin.
		cachePath := filepath.Join(pluginsDir, "cache", pluginHint)
		if content, err := searchCacheDir(cachePath, skillName); err == nil {
			return content, nil
		}
	}

	return "", os.ErrNotExist
}

// searchCacheDir searches a plugin cache directory for a skill.
// Cache structure: {cachePluginDir}/{sub}/{version}/skills/{name}/SKILL.md
func searchCacheDir(cachePluginDir, skillName string) (string, error) {
	subEntries, err := os.ReadDir(cachePluginDir)
	if err != nil {
		return "", err
	}

	for _, sub := range subEntries {
		if !sub.IsDir() {
			continue
		}
		innerDir := filepath.Join(cachePluginDir, sub.Name())

		// Look for versioned dirs inside.
		versionEntries, err := os.ReadDir(innerDir)
		if err == nil {
			for _, ver := range versionEntries {
				if !ver.IsDir() {
					continue
				}
				skillPath := filepath.Join(innerDir, ver.Name(), "skills", skillName, "SKILL.md")
				if content, err := tryReadSkillFile(skillPath); err == nil {
					return content, nil
				}
			}
		}

		// Also check directly (non-versioned).
		directPath := filepath.Join(innerDir, "skills", skillName, "SKILL.md")
		if content, err := tryReadSkillFile(directPath); err == nil {
			return content, nil
		}
	}

	return "", os.ErrNotExist
}

// truncate limits a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// collapseSpaces replaces runs of whitespace with a single space.
func collapseSpaces(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return b.String()
}

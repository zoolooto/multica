package execenv

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Codex's project-instruction budget. Codex concatenates the AGENTS.md files it
// finds (the global file in CODEX_HOME, then the repository root down to the
// working directory) and loads at most project_doc_max_bytes of them; whatever
// does not fit is dropped without an error. The documented default is 32 KiB
// (https://learn.chatgpt.com/docs/agent-configuration/agents-md), and codex-cli
// loads exactly 32,768 instruction bytes from a larger file — see GH #10807.
//
// Multica appends its runtime brief to the working directory's AGENTS.md, which
// is the LAST source in that chain. A brief that overflows the budget therefore
// loses its tail first — Output / delivery rules included — and the agent looks
// noncompliant even though the rules never reached its input. This file measures
// that boundary before launch so the daemon can say so out loud.
const CodexDefaultProjectDocMaxBytes = 32 * 1024

// Budget provenance labels, reported so a warning can name where the effective
// limit came from (an explicit value is the user's to fix, not ours).
const (
	CodexBudgetFromDefault      = "default"
	CodexBudgetFromLaunchArgs   = "launch_args"
	CodexBudgetFromConfigTOML   = "config.toml"
	codexProjectDocMaxBytesKey  = "project_doc_max_bytes"
	codexInstructionFileName    = "AGENTS.md"
	codexSourceListMaxInWarning = 6
)

// CodexInstructionSource is one AGENTS.md file counting against the budget,
// named the way an operator can act on it. Bytes are raw file bytes; no
// instruction content is ever recorded.
type CodexInstructionSource struct {
	Name  string
	Bytes int
}

// CodexInstructionBudget is the pre-launch measurement of what Codex will load.
type CodexInstructionBudget struct {
	// BudgetBytes is the effective project_doc_max_bytes.
	BudgetBytes int
	// BudgetSource is one of the CodexBudgetFrom* labels.
	BudgetSource string
	// TotalBytes is the summed size of every AGENTS.md source Codex reads,
	// in load order (global file first, working-directory file last).
	TotalBytes int
	// Sources lists the files that make up TotalBytes, in load order.
	Sources []CodexInstructionSource
	// TruncatedSections names the Multica brief sections (## headings inside
	// the managed block) that do not fit fully inside the budget. Only section
	// names are recorded — never their text.
	TruncatedSections []string
}

// Overflow reports whether Codex would drop instruction bytes.
func (b CodexInstructionBudget) Overflow() bool { return b.TotalBytes > b.BudgetBytes }

// OverflowBytes is how many bytes Codex would drop (0 when within budget).
func (b CodexInstructionBudget) OverflowBytes() int {
	if !b.Overflow() {
		return 0
	}
	return b.TotalBytes - b.BudgetBytes
}

// CheckCodexInstructionBudget measures the Codex instruction chain for workDir
// against the effective project-doc budget. It is read-only and never fails:
// a diagnostic that cannot read one file still reports the rest, because a
// silent skip would reintroduce exactly the visibility gap this exists to close.
//
// launchArgs must be the normalized arguments Codex will actually launch with
// (execenv consumers pass the same value buildCodexArgs uses), so a `-c
// project_doc_max_bytes=…` override is honoured instead of being reported as an
// overflow the user already configured away.
func CheckCodexInstructionBudget(workDir, codexHome string, launchArgs []string) CodexInstructionBudget {
	budget, source := codexProjectDocMaxBudget(codexHome, launchArgs)
	report := CodexInstructionBudget{BudgetBytes: budget, BudgetSource: source}

	// Codex load order: the global CODEX_HOME file first, then the repository
	// root down to the working directory. The working-directory file is last,
	// and it is the one carrying the managed brief.
	projectDirs := codexProjectInstructionDirs(workDir)
	var files []string
	if codexHome != "" {
		files = append(files, filepath.Join(codexHome, codexInstructionFileName))
	}
	for _, dir := range projectDirs {
		files = append(files, filepath.Join(dir, codexInstructionFileName))
	}
	// Sources are named relative to the repository root so a warning reads
	// "pkg/AGENTS.md", not "../../AGENTS.md".
	repoRoot := workDir
	if len(projectDirs) > 0 {
		repoRoot = projectDirs[0]
	}

	// cumulative is the number of bytes that precede the current file in the
	// concatenation Codex builds; section offsets inside the managed block are
	// absolute only once this prefix is added.
	cumulative := 0
	managedPath := filepath.Join(workDir, codexInstructionFileName)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			// Missing files are the normal case (most repos ship one AGENTS.md,
			// not one per directory). Unreadable ones are skipped for the size
			// total rather than failing the diagnostic.
			continue
		}
		report.Sources = append(report.Sources, CodexInstructionSource{
			Name:  codexInstructionSourceName(path, repoRoot, codexHome),
			Bytes: len(data),
		})
		report.TotalBytes += len(data)
		if path == managedPath {
			report.TruncatedSections = codexTruncatedSections(data, cumulative, budget)
		}
		cumulative += len(data)
	}
	return report
}

// CodexInstructionBudgetNotice returns a human-readable warning when the Codex
// instruction chain overflows the effective budget, and "" otherwise. The text
// names the effective limit, its provenance, the measured total, the brief
// sections that will not reach the agent, and the two ways out. It never
// includes instruction bodies.
func CodexInstructionBudgetNotice(workDir, codexHome string, launchArgs []string) string {
	report := CheckCodexInstructionBudget(workDir, codexHome, launchArgs)
	if !report.Overflow() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Codex instruction budget exceeded: the AGENTS.md files Codex will read total ")
	b.WriteString(strconv.Itoa(report.TotalBytes))
	b.WriteString(" bytes, but the effective project_doc_max_bytes is ")
	b.WriteString(strconv.Itoa(report.BudgetBytes))
	b.WriteString(" bytes (")
	b.WriteString(report.BudgetSource)
	b.WriteString("). Codex loads only the first ")
	b.WriteString(strconv.Itoa(report.BudgetBytes))
	b.WriteString(" bytes and silently drops the remaining ")
	b.WriteString(strconv.Itoa(report.OverflowBytes()))
	b.WriteString(" bytes.")
	if len(report.TruncatedSections) > 0 {
		b.WriteString(" Multica brief sections that will not reach the agent: ")
		b.WriteString(strings.Join(report.TruncatedSections, ", "))
		b.WriteString(".")
	}
	b.WriteString(" Raise the limit (for example `-c project_doc_max_bytes=65536` in the agent's custom args) or shorten the repository and agent instructions.")
	if len(report.Sources) > 0 {
		b.WriteString(" Sources:")
		for i, src := range report.Sources {
			if i == codexSourceListMaxInWarning {
				b.WriteString(" …")
				break
			}
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(" ")
			b.WriteString(src.Name)
			b.WriteString(" (")
			b.WriteString(strconv.Itoa(src.Bytes))
			b.WriteString(" B)")
		}
		b.WriteString(".")
	}
	return b.String()
}

// codexProjectInstructionDirs returns the directories whose AGENTS.md files
// Codex reads, in load order: the repository root first, then each directory
// down to workDir. When workDir sits inside no repository the chain is just
// workDir, matching Codex's "current directory only" behaviour.
func codexProjectInstructionDirs(workDir string) []string {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return nil
	}
	root := abs
	for dir := abs; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			root = dir
			break
		}
		if filepath.Dir(dir) == dir {
			// No repository marker anywhere: Codex still reads the working
			// directory's own AGENTS.md, nothing above it.
			break
		}
	}
	// Walk root → workDir without relying on string prefix checks.
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return []string{abs}
	}
	var dirs []string
	dirs = append(dirs, root)
	if rel != "." {
		current := root
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if part == "" || part == "." {
				continue
			}
			current = filepath.Join(current, part)
			dirs = append(dirs, current)
		}
	}
	return dirs
}

// codexProjectDocMaxBudget resolves the effective project_doc_max_bytes.
// Precedence matches Codex's own: an explicit launch-argument override wins over
// the user's config.toml, which wins over the documented default.
func codexProjectDocMaxBudget(codexHome string, launchArgs []string) (int, string) {
	budget := CodexDefaultProjectDocMaxBytes
	source := CodexBudgetFromDefault
	if value, ok := codexProjectDocMaxFromConfig(codexHome); ok {
		budget = value
		source = CodexBudgetFromConfigTOML
	}
	if value, ok := codexProjectDocMaxFromLaunchArgs(launchArgs); ok {
		budget = value
		source = CodexBudgetFromLaunchArgs
	}
	return budget, source
}

// codexProjectDocMaxFromConfig reads the top-level project_doc_max_bytes from
// the per-task CODEX_HOME config.toml (the effective file after the daemon's
// shared→task copy, so inherited user configuration is covered).
func codexProjectDocMaxFromConfig(codexHome string) (int, bool) {
	if codexHome == "" {
		return 0, false
	}
	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		return 0, false
	}
	var cfg struct {
		ProjectDocMaxBytes *int64 `toml:"project_doc_max_bytes"`
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return 0, false
	}
	if cfg.ProjectDocMaxBytes == nil || *cfg.ProjectDocMaxBytes <= 0 {
		return 0, false
	}
	return int(*cfg.ProjectDocMaxBytes), true
}

// codexProjectDocMaxFromLaunchArgs finds the last `-c` / `--config`
// project_doc_max_bytes override on the launch line, matching Codex's
// last-assignment-wins handling of repeated overrides.
func codexProjectDocMaxFromLaunchArgs(args []string) (int, bool) {
	budget := 0
	found := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		var value string
		switch {
		case arg == "-c" || arg == "--config":
			if i+1 >= len(args) {
				continue
			}
			i++
			value = args[i]
		case strings.HasPrefix(arg, "-c="):
			value = strings.TrimPrefix(arg, "-c=")
		case strings.HasPrefix(arg, "--config="):
			value = strings.TrimPrefix(arg, "--config=")
		default:
			continue
		}
		key, raw, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) != codexProjectDocMaxBytesKey {
			continue
		}
		parsed, err := strconv.Atoi(strings.Trim(strings.TrimSpace(raw), `"'`))
		if err != nil || parsed <= 0 {
			continue
		}
		budget = parsed
		found = true
	}
	return budget, found
}

// codexTruncatedSections returns the brief sections that do not fit entirely
// before the budget, given the file's managed block and the bytes that precede
// it. A section is reported when its end offset (prefix + section range) runs
// past the budget, so a section cut mid-way is reported too — that partial
// section is exactly how "silently truncated" reaches the agent.
func codexTruncatedSections(file []byte, prefix, budget int) []string {
	content := string(file)
	start, end, ok := locateMarkerBlock(content)
	if !ok {
		return nil
	}
	type section struct {
		name  string
		start int // absolute offset in file
	}
	var sections []section
	offset := start
	for offset < end {
		next := strings.IndexByte(content[offset:end], '\n')
		var lineEnd int
		if next < 0 {
			lineEnd = end
		} else {
			lineEnd = offset + next + 1
		}
		if strings.HasPrefix(content[offset:], "## ") {
			name := strings.TrimSpace(content[offset+3 : lineEnd])
			sections = append(sections, section{name: name, start: offset})
		}
		offset = lineEnd
	}
	var truncated []string
	for i, sec := range sections {
		secEnd := end
		if i+1 < len(sections) {
			secEnd = sections[i+1].start
		}
		if prefix+secEnd > budget {
			truncated = append(truncated, sec.name)
		}
	}
	return truncated
}

// codexInstructionSourceName labels a source for the warning: the working
// directory file, the global file, or a path relative to the repository root.
func codexInstructionSourceName(path, repoRoot, codexHome string) string {
	if codexHome != "" && samePath(path, filepath.Join(codexHome, codexInstructionFileName)) {
		return "$CODEX_HOME/" + codexInstructionFileName
	}
	if rel, err := filepath.Rel(repoRoot, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	left, errA := filepath.Abs(a)
	right, errB := filepath.Abs(b)
	return errA == nil && errB == nil && left == right
}

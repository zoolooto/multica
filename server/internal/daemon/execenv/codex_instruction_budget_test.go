package execenv

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeBudgetFixture creates an AGENTS.md of exactly n bytes under dir and
// returns the path. used for the pure size-accounting boundaries.
func writeBudgetFixture(t *testing.T, dir string, n int) string {
	t.Helper()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", n)), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	return path
}

func TestCheckCodexInstructionBudgetBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		bytes      int
		wantTotal  int
		wantOver   bool
		wantBudget int
	}{
		{name: "below default", bytes: CodexDefaultProjectDocMaxBytes - 1, wantTotal: CodexDefaultProjectDocMaxBytes - 1, wantBudget: CodexDefaultProjectDocMaxBytes},
		{name: "at default", bytes: CodexDefaultProjectDocMaxBytes, wantTotal: CodexDefaultProjectDocMaxBytes, wantBudget: CodexDefaultProjectDocMaxBytes},
		{name: "above default", bytes: CodexDefaultProjectDocMaxBytes + 1, wantTotal: CodexDefaultProjectDocMaxBytes + 1, wantOver: true, wantBudget: CodexDefaultProjectDocMaxBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeBudgetFixture(t, dir, tc.bytes)
			report := CheckCodexInstructionBudget(dir, "", nil)
			if report.BudgetBytes != tc.wantBudget {
				t.Fatalf("budget = %d, want %d", report.BudgetBytes, tc.wantBudget)
			}
			if report.BudgetSource != CodexBudgetFromDefault {
				t.Fatalf("budget source = %q, want %q", report.BudgetSource, CodexBudgetFromDefault)
			}
			if report.TotalBytes != tc.wantTotal {
				t.Fatalf("total = %d, want %d", report.TotalBytes, tc.wantTotal)
			}
			if report.Overflow() != tc.wantOver {
				t.Fatalf("overflow = %v, want %v", report.Overflow(), tc.wantOver)
			}
			if got, want := report.OverflowBytes(), tc.wantTotal-tc.wantBudget; tc.wantOver && got != want {
				t.Fatalf("overflow bytes = %d, want %d", got, want)
			}
		})
	}
}

func TestCheckCodexInstructionBudgetLaunchArgOverride(t *testing.T) {
	dir := t.TempDir()
	writeBudgetFixture(t, dir, CodexDefaultProjectDocMaxBytes+1024)

	report := CheckCodexInstructionBudget(dir, "", []string{"-c", "project_doc_max_bytes=65536"})
	if report.Overflow() {
		t.Fatalf("65536-byte override should fit %d bytes", report.TotalBytes)
	}
	if report.BudgetBytes != 65536 || report.BudgetSource != CodexBudgetFromLaunchArgs {
		t.Fatalf("budget = %d (%s), want 65536 (launch_args)", report.BudgetBytes, report.BudgetSource)
	}

	// A later assignment wins, matching Codex's own handling.
	report = CheckCodexInstructionBudget(dir, "", []string{"-c", "project_doc_max_bytes=65536", "--config", "project_doc_max_bytes=1024"})
	if !report.Overflow() || report.BudgetBytes != 1024 {
		t.Fatalf("last override should win: budget = %d overflow = %v", report.BudgetBytes, report.Overflow())
	}

	// The `-c=value` spelling is accepted too.
	report = CheckCodexInstructionBudget(dir, "", []string{"-c=project_doc_max_bytes=65536"})
	if report.Overflow() || report.BudgetBytes != 65536 {
		t.Fatalf("-c=value override not honoured: budget = %d overflow = %v", report.BudgetBytes, report.Overflow())
	}
}

func TestCheckCodexInstructionBudgetConfigTOMLOverride(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	writeBudgetFixture(t, dir, 40000)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("project_doc_max_bytes = 40000\n"), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	report := CheckCodexInstructionBudget(dir, codexHome, nil)
	if report.Overflow() {
		t.Fatalf("config.toml override should fit %d bytes", report.TotalBytes)
	}
	if report.BudgetBytes != 40000 || report.BudgetSource != CodexBudgetFromConfigTOML {
		t.Fatalf("budget = %d (%s), want 40000 (config.toml)", report.BudgetBytes, report.BudgetSource)
	}

	// An explicit launch argument still wins over the inherited config.
	report = CheckCodexInstructionBudget(dir, codexHome, []string{"-c", "project_doc_max_bytes=32768"})
	if !report.Overflow() || report.BudgetSource != CodexBudgetFromLaunchArgs {
		t.Fatalf("launch arg must beat config.toml: budget = %d (%s) overflow = %v", report.BudgetBytes, report.BudgetSource, report.Overflow())
	}
}

func TestCheckCodexInstructionBudgetCountsRepositoryPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(strings.Repeat("r", 5000)), 0o644); err != nil {
		t.Fatalf("write repo AGENTS.md: %v", err)
	}
	if _, err := InjectRuntimeConfig(dir, "codex", TaskContextForEnv{IssueID: "budget-prefix"}); err != nil {
		t.Fatalf("InjectRuntimeConfig: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("stat AGENTS.md: %v", err)
	}

	// A generous explicit limit keeps the assertion about accounting, not about
	// how large the generated brief happens to be today.
	report := CheckCodexInstructionBudget(dir, "", []string{"-c", "project_doc_max_bytes=1048576"})
	if report.TotalBytes != int(fi.Size()) {
		t.Fatalf("total = %d, want injected file size %d (no double counting)", report.TotalBytes, fi.Size())
	}
	if report.Overflow() {
		t.Fatalf("a %d-byte file should fit a 1 MiB budget", fi.Size())
	}
	if len(report.Sources) != 1 || report.Sources[0].Name != "AGENTS.md" {
		t.Fatalf("sources = %v, want only the working-directory AGENTS.md", report.Sources)
	}
}

func TestCheckCodexInstructionBudgetCountsAncestorsUpToRepoRoot(t *testing.T) {
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	work := filepath.Join(repo, "pkg", "work")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	// Above the repository root: not part of Codex's project-instruction chain.
	if err := os.WriteFile(filepath.Join(outer, "AGENTS.md"), []byte(strings.Repeat("o", 500)), 0o644); err != nil {
		t.Fatalf("write outer AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(strings.Repeat("r", 100)), 0o644); err != nil {
		t.Fatalf("write repo AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "AGENTS.md"), []byte(strings.Repeat("p", 200)), 0o644); err != nil {
		t.Fatalf("write pkg AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "AGENTS.md"), []byte(strings.Repeat("w", 300)), 0o644); err != nil {
		t.Fatalf("write work AGENTS.md: %v", err)
	}

	report := CheckCodexInstructionBudget(work, "", nil)
	if report.TotalBytes != 600 {
		t.Fatalf("total = %d, want 600 (100 root + 200 pkg + 300 work, outer excluded)", report.TotalBytes)
	}
	if len(report.Sources) != 3 {
		t.Fatalf("sources = %v, want 3 in load order", report.Sources)
	}
	wantNames := []string{"AGENTS.md", "pkg/AGENTS.md", "pkg/work/AGENTS.md"}
	for i, want := range wantNames {
		if report.Sources[i].Name != want {
			t.Fatalf("source[%d] = %q, want %q", i, report.Sources[i].Name, want)
		}
	}
}

func TestCheckCodexInstructionBudgetCountsGlobalFile(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	writeBudgetFixture(t, dir, 1000)
	if err := os.WriteFile(filepath.Join(codexHome, "AGENTS.md"), []byte(strings.Repeat("g", 250)), 0o644); err != nil {
		t.Fatalf("write global AGENTS.md: %v", err)
	}

	report := CheckCodexInstructionBudget(dir, codexHome, nil)
	if report.TotalBytes != 1250 {
		t.Fatalf("total = %d, want 1250 (250 global + 1000 workdir)", report.TotalBytes)
	}
	if len(report.Sources) != 2 || report.Sources[0].Name != "$CODEX_HOME/AGENTS.md" {
		t.Fatalf("global file should lead the chain: %v", report.Sources)
	}
}

func TestCheckCodexInstructionBudgetCountsUTF8Bytes(t *testing.T) {
	dir := t.TempDir()
	// 1000 three-byte runes: 3000 bytes, not 1000.
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(strings.Repeat("中", 1000)), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	report := CheckCodexInstructionBudget(dir, "", nil)
	if report.TotalBytes != 3000 {
		t.Fatalf("total = %d, want 3000 bytes", report.TotalBytes)
	}
}

func TestCodexInstructionBudgetNoticeReportsTruncatedSections(t *testing.T) {
	dir := t.TempDir()
	const sentinel = "SECRET-INSTRUCTION-BODY-SENTINEL"
	ctx := TaskContextForEnv{
		IssueID:     "budget-notice",
		AgentSkills: []SkillContextForEnv{{Name: "Review", Content: sentinel}},
	}
	if _, err := InjectRuntimeConfig(dir, "codex", ctx); err != nil {
		t.Fatalf("InjectRuntimeConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	outputOffset := strings.Index(string(data), "\n## Output\n")
	if outputOffset < 0 {
		t.Fatalf("injected brief has no Output section:\n%s", data)
	}

	// A budget that ends inside the Output section: every section after it,
	// plus Output itself, is not fully loaded.
	report := CheckCodexInstructionBudget(dir, "", []string{"-c", "project_doc_max_bytes=" + strconv.Itoa(outputOffset+10)})
	if !report.Overflow() {
		t.Fatalf("expected overflow for a budget inside the Output section")
	}
	if len(report.TruncatedSections) == 0 {
		t.Fatalf("expected truncated sections, got none")
	}
	foundOutput := false
	for _, name := range report.TruncatedSections {
		if name == "Output" {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Fatalf("Output should be reported as truncated, got %v", report.TruncatedSections)
	}

	notice := CodexInstructionBudgetNotice(dir, "", []string{"-c", "project_doc_max_bytes=" + strconv.Itoa(outputOffset+10)})
	if notice == "" {
		t.Fatalf("expected a notice")
	}
	for _, want := range []string{"project_doc_max_bytes", "Output", "AGENTS.md"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice missing %q: %s", want, notice)
		}
	}
	if strings.Contains(notice, sentinel) {
		t.Fatalf("notice must not include instruction bodies: %s", notice)
	}
}

func TestCodexInstructionBudgetNoticeSilentWithinBudget(t *testing.T) {
	dir := t.TempDir()
	if _, err := InjectRuntimeConfig(dir, "codex", TaskContextForEnv{IssueID: "budget-quiet"}); err != nil {
		t.Fatalf("InjectRuntimeConfig: %v", err)
	}
	if notice := CodexInstructionBudgetNotice(dir, "", nil); notice != "" {
		t.Fatalf("expected no notice within budget, got %q", notice)
	}
}

func TestCodexInstructionBudgetRepeatedInjectionStable(t *testing.T) {
	dir := t.TempDir()
	ctx := TaskContextForEnv{IssueID: "budget-stable"}
	if _, err := InjectRuntimeConfig(dir, "codex", ctx); err != nil {
		t.Fatalf("first InjectRuntimeConfig: %v", err)
	}
	first := CheckCodexInstructionBudget(dir, "", nil).TotalBytes
	if _, err := InjectRuntimeConfig(dir, "codex", ctx); err != nil {
		t.Fatalf("second InjectRuntimeConfig: %v", err)
	}
	second := CheckCodexInstructionBudget(dir, "", nil).TotalBytes
	if first != second {
		t.Fatalf("re-injection changed the measured size: %d -> %d", first, second)
	}
}

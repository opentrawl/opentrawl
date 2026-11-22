package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	genericExportedNames = map[string]bool{
		"Data": true, "Info": true, "Value": true, "Result": true, "State": true,
		"Summary": true, "Config": true, "Helper": true, "Manager": true,
		"Process": true, "Handle": true, "Item": true, "Record": true,
		"Entry": true, "Util": true, "Utils": true,
	}
	genericFileNames = map[string]bool{
		"util.go": true, "utils.go": true, "helper.go": true, "helpers.go": true,
		"common.go": true, "misc.go": true, "types.go": true, "models.go": true,
		"manager.go": true, "handler.go": true, "process.go": true,
	}
	syntheticTestPattern = regexp.MustCompile(`(?i)example\.com|Avery Example|Bob Baker|\+1555|testdata|fixture`)
	swiftUnsafePattern   = regexp.MustCompile(`\bAny\b|\bas!|\btry!`)
)

type areaStats struct {
	prodLOC int
	testLOC int
	genLOC  int
	files   int
}

type funcMetric struct {
	file       string
	line       int
	name       string
	lines      int
	complexity int
}

type finding struct {
	file   string
	line   int
	detail string
}

func main() {
	checkBoundariesOnly := flag.Bool("check-boundaries", false, "exit non-zero if any Go architecture boundary violation is found")
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve repo root: %v\n", err)
		os.Exit(2)
	}

	trackedFiles, err := gitTrackedFiles(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list tracked files: %v\n", err)
		os.Exit(2)
	}

	var (
		areas              = map[string]*areaStats{}
		boundaryViolations []finding
		genericExports     []finding
		genericFiles       []string
		nolintFindings     []finding
		anyByFile          = map[string]int{}
		mapAnyCount        int
		totalAnyCount      int
		funcMetrics        []funcMetric
		goTestFiles        int
		goInternalTests    int
		goSyntheticTests   int
		swiftFiles         []funcMetric
		swiftUnsafeLines   int
		swiftTestFiles     int
	)

	fset := token.NewFileSet()
	for _, rel := range trackedFiles {
		full := filepath.Join(absRoot, rel)
		ext := filepath.Ext(rel)
		if ext != ".go" && ext != ".swift" && ext != ".proto" && ext != ".m" && ext != ".h" && ext != ".py" && ext != ".sh" {
			continue
		}
		lines, err := countLines(full)
		if err != nil {
			continue
		}
		area := classifyArea(absRoot, rel)
		stat := areas[area]
		if stat == nil {
			stat = &areaStats{}
			areas[area] = stat
		}
		stat.files++

		isGenerated := strings.HasSuffix(rel, ".pb.go") || strings.HasSuffix(rel, ".pb.swift")
		isTest := strings.HasSuffix(rel, "_test.go") || strings.HasSuffix(rel, "Tests.swift") || strings.HasSuffix(rel, "_test.py") || strings.Contains(rel, "/Tests/")
		switch {
		case isGenerated:
			stat.genLOC += lines
		case isTest:
			stat.testLOC += lines
		default:
			stat.prodLOC += lines
		}

		if ext == ".swift" && !isGenerated {
			if isTest {
				swiftTestFiles++
				continue
			}
			swiftFiles = append(swiftFiles, funcMetric{file: rel, lines: lines})
			swiftUnsafeLines += countSwiftUnsafeLines(full)
			continue
		}

		if ext != ".go" || isGenerated {
			continue
		}
		if isTest {
			goTestFiles++
			if strings.Contains(rel, "/internal/") {
				goInternalTests++
			}
			if fileMatchesPattern(full, syntheticTestPattern) {
				goSyntheticTests++
			}
			continue
		}

		if genericFileNames[strings.ToLower(filepath.Base(rel))] {
			genericFiles = append(genericFiles, rel)
		}

		parsed, err := parser.ParseFile(fset, full, nil, parser.ParseComments)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", rel, err)
			continue
		}

		// 1. Architecture boundary checks from real AST imports.
		for _, imp := range parsed.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			line := fset.Position(imp.Pos()).Line
			if violation := checkImportBoundary(rel, importPath); violation != "" {
				boundaryViolations = append(boundaryViolations, finding{
					file: rel, line: line, detail: violation + " (" + importPath + ")",
				})
			}
		}

		// 2. Nolint suppressions from real comments.
		for _, cg := range parsed.Comments {
			for _, c := range cg.List {
				if strings.Contains(c.Text, "nolint") {
					nolintFindings = append(nolintFindings, finding{
						file: rel, line: fset.Position(c.Pos()).Line, detail: strings.TrimSpace(c.Text),
					})
				}
			}
		}

		// 3. Exported generic names, any/interface{}, and function complexity.
		for _, decl := range parsed.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && genericExportedNames[ts.Name.Name] {
						genericExports = append(genericExports, finding{
							file: rel, line: fset.Position(ts.Pos()).Line, detail: "type " + ts.Name.Name,
						})
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil && genericExportedNames[d.Name.Name] {
					genericExports = append(genericExports, finding{
						file: rel, line: fset.Position(d.Pos()).Line, detail: "func " + d.Name.Name,
					})
				}
				if d.Body != nil {
					startLine := fset.Position(d.Pos()).Line
					endLine := fset.Position(d.End()).Line
					funcMetrics = append(funcMetrics, funcMetric{
						file:       rel,
						line:       startLine,
						name:       formatFuncName(d),
						lines:      endLine - startLine + 1,
						complexity: cognitiveComplexity(d.Body),
					})
				}
			}
		}

		ast.Inspect(parsed, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.InterfaceType:
				if node.Methods == nil || len(node.Methods.List) == 0 {
					anyByFile[rel]++
					totalAnyCount++
				}
			case *ast.Ident:
				if node.Name == "any" && node.Obj == nil {
					anyByFile[rel]++
					totalAnyCount++
				}
			case *ast.MapType:
				if isStringKey(node.Key) && isEmptyInterfaceOrAny(node.Value) {
					mapAnyCount++
				}
			}
			return true
		})
	}

	if *checkBoundariesOnly {
		if len(boundaryViolations) > 0 {
			fmt.Fprintln(os.Stderr, "architecture boundary violations:")
			for _, v := range boundaryViolations {
				fmt.Fprintf(os.Stderr, "  %s:%d: %s\n", v.file, v.line, v.detail)
			}
			os.Exit(1)
		}
		fmt.Println("boundary-check: ok (0 cross-trawler or inward-layer violations)")
		return
	}

	printReport(
		areas,
		boundaryViolations,
		genericFiles,
		genericExports,
		nolintFindings,
		totalAnyCount,
		mapAnyCount,
		anyByFile,
		funcMetrics,
		swiftFiles,
		swiftUnsafeLines,
		goTestFiles,
		goInternalTests,
		goSyntheticTests,
		swiftTestFiles,
	)
}

func checkImportBoundary(relFile, importPath string) string {
	const modulePrefix = "github.com/opentrawl/opentrawl/"
	if !strings.HasPrefix(importPath, modulePrefix) {
		return ""
	}
	sub := strings.TrimPrefix(importPath, modulePrefix)
	if strings.HasPrefix(relFile, "trawlers/") {
		own := strings.Split(relFile, "/")[1]
		if sub == "trawl" || strings.HasPrefix(sub, "trawl/") {
			return "trawler must not import trawl CLI"
		}
		targetTrawler := trawlerNameFromImport(sub)
		if targetTrawler != "" && targetTrawler != own {
			return "cross-trawler import from " + own + " to " + targetTrawler
		}
	}
	if strings.HasPrefix(relFile, "trawl/") {
		if strings.Contains(sub, "/internal/") && !strings.HasPrefix(sub, "trawl/internal/") {
			return "trawl CLI must not import trawler internal package"
		}
	}
	if strings.HasPrefix(relFile, "trawlkit/") {
		if sub == "trawl" || strings.HasPrefix(sub, "trawl/") || trawlerNameFromImport(sub) != "" {
			return "trawlkit must not depend on trawl CLI or a trawler"
		}
	}
	return ""
}

func trawlerNameFromImport(sub string) string {
	if strings.HasPrefix(sub, "trawlers/") {
		parts := strings.Split(sub, "/")
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	first := strings.Split(sub, "/")[0]
	switch first {
	case "calendar", "gmail", "twitter":
		return first
	default:
		return ""
	}
}

func classifyArea(root, rel string) string {
	parts := strings.Split(rel, "/")
	switch {
	case parts[0] == "trawlers" && len(parts) > 1:
		return "trawlers/" + parts[1]
	case parts[0] == "trawlkit" && len(parts) > 1:
		if info, err := os.Stat(filepath.Join(root, "trawlkit", parts[1])); err == nil && info.IsDir() {
			return "trawlkit/" + parts[1]
		}
		return "trawlkit (root)"
	case parts[0] == "app" && len(parts) > 2 && parts[1] == "Sources":
		return "app/Sources/" + parts[2]
	case parts[0] == "app" && len(parts) > 1:
		return "app/" + parts[1]
	default:
		return parts[0]
	}
}

func cognitiveComplexity(body *ast.BlockStmt) int {
	score := 0
	var walk func(node ast.Node, nesting int)
	walk = func(node ast.Node, nesting int) {
		if node == nil {
			return
		}
		switch n := node.(type) {
		case *ast.IfStmt:
			score += 1 + nesting
			walk(n.Body, nesting+1)
			if n.Else != nil {
				if elseIf, ok := n.Else.(*ast.IfStmt); ok {
					score++
					walk(elseIf.Body, nesting+1)
					walk(elseIf.Else, nesting)
				} else {
					score++
					walk(n.Else, nesting+1)
				}
			}
			return
		case *ast.ForStmt:
			score += 1 + nesting
			walk(n.Body, nesting+1)
			return
		case *ast.RangeStmt:
			score += 1 + nesting
			walk(n.Body, nesting+1)
			return
		case *ast.SwitchStmt:
			score += 1 + nesting
			walk(n.Body, nesting+1)
			return
		case *ast.TypeSwitchStmt:
			score += 1 + nesting
			walk(n.Body, nesting+1)
			return
		case *ast.SelectStmt:
			score += 1 + nesting
			walk(n.Body, nesting+1)
			return
		case *ast.FuncLit:
			walk(n.Body, nesting+1)
			return
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				score++
			}
		}
		ast.Inspect(node, func(child ast.Node) bool {
			if child == nil || child == node {
				return true
			}
			switch child.(type) {
			case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.FuncLit, *ast.BinaryExpr:
				walk(child, nesting)
				return false
			}
			return true
		})
	}
	walk(body, 0)
	return score
}

func formatFuncName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}
	return "(" + exprText(d.Recv.List[0].Type) + ")." + d.Name.Name
}

func exprText(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprText(t.X)
	case *ast.IndexExpr:
		return exprText(t.X)
	default:
		return "?"
	}
}

func isStringKey(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "string"
}

func isEmptyInterfaceOrAny(expr ast.Expr) bool {
	if id, ok := expr.(*ast.Ident); ok && id.Name == "any" {
		return true
	}
	if iface, ok := expr.(*ast.InterfaceType); ok && (iface.Methods == nil || len(iface.Methods.List) == 0) {
		return true
	}
	return false
}

func gitTrackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	n := 0
	for scanner.Scan() {
		n++
	}
	return n, scanner.Err()
}

func fileMatchesPattern(path string, pattern *regexp.Regexp) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return pattern.Match(data)
}

func countSwiftUnsafeLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "//") {
			continue
		}
		if swiftUnsafePattern.MatchString(line) {
			count++
		}
	}
	return count
}

func printReport(
	areas map[string]*areaStats,
	boundaryViolations []finding,
	genericFiles []string,
	genericExports []finding,
	nolintFindings []finding,
	totalAnyCount int,
	mapAnyCount int,
	anyByFile map[string]int,
	funcMetrics []funcMetric,
	swiftFiles []funcMetric,
	swiftUnsafeLines int,
	goTestFiles int,
	goInternalTests int,
	goSyntheticTests int,
	swiftTestFiles int,
) {
	fmt.Println("=== OpenTrawl Objective Health Scorecard ===")
	fmt.Println()
	fmt.Printf("%-32s %7s %6s %7s %5s\n", "AREA", "PROD", "TEST", "GEN", "FILES")
	fmt.Println(strings.Repeat("-", 62))
	type areaRow struct {
		name string
		stat *areaStats
	}
	rows := make([]areaRow, 0, len(areas))
	var tot areaStats
	for name, st := range areas {
		rows = append(rows, areaRow{name: name, stat: st})
		tot.prodLOC += st.prodLOC
		tot.testLOC += st.testLOC
		tot.genLOC += st.genLOC
		tot.files += st.files
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].stat.prodLOC > rows[j].stat.prodLOC })
	for _, r := range rows {
		fmt.Printf("%-32s %7d %6d %7d %5d\n", r.name, r.stat.prodLOC, r.stat.testLOC, r.stat.genLOC, r.stat.files)
	}
	fmt.Println(strings.Repeat("-", 62))
	fmt.Printf("%-32s %7d %6d %7d %5d\n", "TOTAL", tot.prodLOC, tot.testLOC, tot.genLOC, tot.files)

	fmt.Println()
	fmt.Println("=== Summary Metrics ===")
	fmt.Printf("  Architecture boundary violations:  %d\n", len(boundaryViolations))
	fmt.Printf("  Go //nolint suppressions:          %d\n", len(nolintFindings))
	fmt.Printf("  Go prod any / interface{}:         %d (map[string]any: %d)\n", totalAnyCount, mapAnyCount)
	fmt.Printf("  Generic junk-drawer Go filenames:  %d\n", len(genericFiles))
	fmt.Printf("  Bare generic exported Go symbols:  %d\n", len(genericExports))
	fmt.Printf("  Swift Any / as! / try! lines:      %d\n", swiftUnsafeLines)
	fmt.Printf("  Go test files:                     %d (%d in internal/, %d with synthetic/fixture markers)\n", goTestFiles, goInternalTests, goSyntheticTests)
	fmt.Printf("  Swift test files:                  %d\n", swiftTestFiles)

	sort.Slice(funcMetrics, func(i, j int) bool { return funcMetrics[i].complexity > funcMetrics[j].complexity })
	fmt.Println()
	fmt.Println("=== Top 10 Go Cognitive Complexity Hotspots ===")
	for i := 0; i < len(funcMetrics) && i < 10; i++ {
		m := funcMetrics[i]
		fmt.Printf("  cog=%3d  lines=%3d  %s:%d  %s\n", m.complexity, m.lines, m.file, m.line, m.name)
	}

	sort.Slice(funcMetrics, func(i, j int) bool { return funcMetrics[i].lines > funcMetrics[j].lines })
	fmt.Println()
	fmt.Println("=== Top 10 Longest Go Functions ===")
	for i := 0; i < len(funcMetrics) && i < 10; i++ {
		m := funcMetrics[i]
		fmt.Printf("  lines=%3d  cog=%3d  %s:%d  %s\n", m.lines, m.complexity, m.file, m.line, m.name)
	}

	sort.Slice(swiftFiles, func(i, j int) bool { return swiftFiles[i].lines > swiftFiles[j].lines })
	fmt.Println()
	fmt.Println("=== Top 5 Largest Swift Files ===")
	for i := 0; i < len(swiftFiles) && i < 5; i++ {
		fmt.Printf("  lines=%4d  %s\n", swiftFiles[i].lines, swiftFiles[i].file)
	}

	if len(genericExports) > 0 {
		fmt.Println()
		fmt.Println("=== Bare Generic Exported Go Symbols ===")
		for _, g := range genericExports {
			fmt.Printf("  %s:%d  %s\n", g.file, g.line, g.detail)
		}
	}

	if len(genericFiles) > 0 {
		sort.Strings(genericFiles)
		fmt.Println()
		fmt.Println("=== Generic Junk-Drawer Go Filenames ===")
		for _, f := range genericFiles {
			fmt.Printf("  %s\n", f)
		}
	}

	type fileCount struct {
		file  string
		count int
	}
	anyRows := make([]fileCount, 0, len(anyByFile))
	for f, c := range anyByFile {
		anyRows = append(anyRows, fileCount{file: f, count: c})
	}
	sort.Slice(anyRows, func(i, j int) bool { return anyRows[i].count > anyRows[j].count })
	if len(anyRows) > 0 {
		fmt.Println()
		fmt.Println("=== Top 10 Go Files Using any / interface{} ===")
		for i := 0; i < len(anyRows) && i < 10; i++ {
			fmt.Printf("  %3d  %s\n", anyRows[i].count, anyRows[i].file)
		}
	}
}

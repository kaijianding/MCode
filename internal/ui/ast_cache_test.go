package mcodeui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type uiProdFile struct {
	Name string
	Src  []byte
	Fset *token.FileSet
	File *ast.File
}

var (
	uiProdFilesOnce      sync.Once
	uiProdFilesCached    []*uiProdFile
	uiProdFilesByNameMap map[string]*uiProdFile
	uiProdFilesErr       error
	uiProdParseCalls     atomic.Int32
)

func loadUIProdFiles() {
	uiProdParseCalls.Add(1)
	entries, err := os.ReadDir(".")
	if err != nil {
		uiProdFilesErr = err
		return
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	files := make([]*uiProdFile, 0, len(names))
	byName := make(map[string]*uiProdFile, len(names))
	for _, name := range names {
		src, err := os.ReadFile(name)
		if err != nil {
			uiProdFilesErr = err
			return
		}
		// 每个文件使用独立 FileSet，保证 byte offset 与 int(comment.Slash)-1 语义一致。
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			uiProdFilesErr = err
			return
		}
		pf := &uiProdFile{
			Name: name,
			Src:  src,
			Fset: fset,
			File: file,
		}
		files = append(files, pf)
		byName[name] = pf
	}
	uiProdFilesCached = files
	uiProdFilesByNameMap = byName
}

// uiProdFiles 返回 internal/ui 当前目录下所有非测试 Go 文件的进程级解析缓存。
func uiProdFiles(t *testing.T) []*uiProdFile {
	t.Helper()
	uiProdFilesOnce.Do(loadUIProdFiles)
	if uiProdFilesErr != nil {
		t.Fatalf("加载并解析 internal/ui 生产文件失败: %v", uiProdFilesErr)
	}
	return uiProdFilesCached
}

// uiProdFileByName 从进程级缓存按文件名（如 "tokens.go"）取生产文件解析结果。
func uiProdFileByName(t *testing.T, name string) *uiProdFile {
	t.Helper()
	uiProdFiles(t)
	pf := uiProdFilesByNameMap[name]
	if pf == nil {
		t.Fatalf("未找到生产文件 %s", name)
	}
	return pf
}

func TestUIProdFilesCache(t *testing.T) {
	before := uiProdParseCalls.Load()
	first := uiProdFiles(t)
	if len(first) == 0 {
		t.Fatal("未加载到任何 internal/ui 生产文件")
	}
	afterFirst := uiProdParseCalls.Load()
	second := uiProdFiles(t)
	if uiProdParseCalls.Load() != afterFirst {
		t.Fatalf("缓存未命中：解析次数从 %d 变为 %d", afterFirst, uiProdParseCalls.Load())
	}
	if len(first) != len(second) || first[0] != second[0] {
		t.Fatal("二次调用应返回同一份缓存切片")
	}
	if before == 0 && afterFirst != 1 {
		t.Fatalf("首次加载应只解析一次，实际 %d 次", afterFirst)
	}
	for _, pf := range first {
		if strings.HasSuffix(pf.Name, "_test.go") || !strings.HasSuffix(pf.Name, ".go") {
			t.Fatalf("缓存包含非生产文件: %s", pf.Name)
		}
		if pf.Fset == nil || pf.File == nil || len(pf.Src) == 0 {
			t.Fatalf("缓存条目字段不完整: %s", pf.Name)
		}
	}
	if got := uiProdFileByName(t, "tokens.go"); got == nil || got.File == nil {
		t.Fatal("uiProdFileByName(\"tokens.go\") 未命中")
	}
}

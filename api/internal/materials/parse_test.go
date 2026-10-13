package materials

import (
	"errors"
	"strings"
	"testing"
)

func TestAllowedExtension(t *testing.T) {
	ok := []string{"a.txt", "a.md", "A.MD", "路径/讲义.md"}
	for _, name := range ok {
		if !AllowedExtension(name) {
			t.Fatalf("%q 应在白名单内", name)
		}
	}
	bad := []string{"a.pdf", "a.docx", "a.exe", "a.txt.exe", "a", "a.md.bak"}
	for _, name := range bad {
		if AllowedExtension(name) {
			t.Fatalf("%q 不应在白名单内", name)
		}
	}
}

func TestParseAcceptsUTF8AndStripsBOM(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("  一元二次方程  \n")...)
	text, err := Parse("讲义.md", raw)
	if err != nil {
		t.Fatalf("合法 UTF-8 应解析成功: %v", err)
	}
	if text != "一元二次方程" {
		t.Fatalf("应去除 BOM 与首尾空白，实际 %q", text)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		file string
		raw  []byte
		want error
	}{
		{"白名单外扩展名", "a.pdf", []byte("x"), ErrUnsupportedType},
		{"空文件", "a.md", nil, ErrEmptyContent},
		{"仅空白", "a.txt", []byte("   \n\t "), ErrEmptyContent},
		{"非 UTF-8", "a.md", []byte{0xff, 0xfe, 0xfd}, ErrUnparsable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(c.file, c.raw); !errors.Is(err, c.want) {
				t.Fatalf("期望 %v，实际 %v", c.want, err)
			}
		})
	}
}

func TestSafeStoredNameStripsPaths(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd.txt":  "passwd.txt",
		"..\\..\\windows\\a.md": "a.md",
		"讲义.md":                 "upload.md",
		"ok_name-1.txt":         "ok_name-1.txt",
	}
	for in, want := range cases {
		if got := SafeStoredName(in); got != want {
			t.Fatalf("SafeStoredName(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestSafeStoredNameNeverKeepsSeparators(t *testing.T) {
	for _, in := range []string{"a/b/c.txt", "..%2f..%2fx.md", "a\\b.md"} {
		got := SafeStoredName(in)
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
			t.Fatalf("存储名不应含路径分隔符或上级引用: %q -> %q", in, got)
		}
	}
}

func TestTitleFromFilenameKeepsChinese(t *testing.T) {
	if got := TitleFromFilename("三角函数讲义.md"); got != "三角函数讲义" {
		t.Fatalf("标题应保留中文，实际 %q", got)
	}
	if got := TitleFromFilename("   "); got != "未命名材料" {
		t.Fatalf("空名应有兜底标题，实际 %q", got)
	}
}

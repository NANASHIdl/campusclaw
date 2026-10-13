package materials

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ErrUnsupportedType / ErrEmptyContent / ErrUnparsable 对应上传失败的三种原因。
var (
	ErrUnsupportedType = errors.New("不支持的扩展名")
	ErrEmptyContent    = errors.New("文件内容为空")
	ErrUnparsable      = errors.New("内容无法解析为文本")
)

var allowedExt = map[string]bool{".txt": true, ".md": true}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// AllowedExtension 判断扩展名是否在白名单内（仅 .txt / .md）。
func AllowedExtension(name string) bool {
	return allowedExt[strings.ToLower(filepath.Ext(name))]
}

// Parse 把白名单内的字节解析为正文。
// 本课只处理 UTF-8 纯文本；不支持的类型、空内容或非法编码一律返回错误，
// 由调用方在落盘/写库之前拒绝。
func Parse(filename string, raw []byte) (string, error) {
	if !AllowedExtension(filename) {
		return "", ErrUnsupportedType
	}
	raw = bytes.TrimPrefix(raw, utf8BOM)
	if !utf8.Valid(raw) {
		return "", ErrUnparsable
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", ErrEmptyContent
	}
	return text, nil
}

// SafeStoredName 生成由服务端掌控的存储文件名。
//
// 只保留 ASCII 字母数字与 - _，不含任何路径分隔符、"." 或 ".."；
// 扩展名单独取自原始文件的后缀。因此客户端提供的路径与文件名
// 无法影响实际写入位置。
func SafeStoredName(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))

	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), "-_")
	if name == "" {
		name = "upload"
	}
	return name + ext
}

// TitleFromFilename 由原始文件名得到展示标题（保留中文）。
func TitleFromFilename(filename string) string {
	base := filepath.Base(filename)
	title := strings.TrimSpace(strings.TrimSuffix(base, filepath.Ext(base)))
	if title == "" {
		return "未命名材料"
	}
	return title
}

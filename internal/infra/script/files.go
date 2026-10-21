package script

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/net/html/charset"
)

const (
	// archiveTempFolder 对齐 Legado ArchiveUtils.TEMP_FOLDER_NAME。
	archiveTempFolder = "ArchiveTemp"
	// maxArchiveBytes 是单次解压/读取压缩包条目的总字节上限，防止压缩炸弹。
	maxArchiveBytes = 64 << 20
	// maxArchiveEntries 是单次解压的条目数上限。
	maxArchiveEntries = 4096
)

var (
	errFileSandboxDisabled = fmt.Errorf("%w: local file APIs require a configured file root", ErrScriptUnsupported)
	errIllegalPath         = errors.New("非法路径")
	fileSuffixPattern      = regexp.MustCompile(`^[a-zA-Z0-9]+$`)
)

// fileSandbox 把 Legado 基于 externalCache 的文件 API 限定在 Policy.FileRoot 内。
type fileSandbox struct {
	root string
}

func newFileSandbox(root string) *fileSandbox {
	root = strings.TrimSpace(root)
	if root == "" {
		return &fileSandbox{}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &fileSandbox{root: filepath.Clean(root)}
}

// resolve 对齐 JsExtensions.getFile：相对路径（可带前导分隔符）拼到根目录下，Clean 后必须仍在根内。
// 已存在组件中的符号链接会被 EvalSymlinks 解析，真实路径逃出沙箱根时报非法路径（S04-01 纵深防御）。
// 目标（或根目录自身）不存在时，只校验最近一个已存在祖先的真实路径仍在 root 内。
func (s *fileSandbox) resolve(rel string) (string, error) {
	if s.root == "" {
		return "", errFileSandboxDisabled
	}
	cleaned := filepath.Clean(filepath.Join(s.root, filepath.FromSlash(rel)))
	if cleaned != s.root && !strings.HasPrefix(cleaned, s.root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", errIllegalPath, rel)
	}
	real, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		// 目标不存在：从最近一个已存在祖先向下校验（祖先不存在说明词法路径未创建，
		// WriteFile/MkdirAll 的落点仍在词法路径内）。
		real = s.resolveExistingAncestor(cleaned)
	}
	if real != s.root && !strings.HasPrefix(real, s.root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", errIllegalPath, rel)
	}
	return cleaned, nil
}

// resolveExistingAncestor 返回 target 最近一个已存在祖先经 EvalSymlinks 解析的真实路径
// 与 target 未存在尾段的拼接；没有已存在祖先时返回词法 target。
func (s *fileSandbox) resolveExistingAncestor(target string) string {
	probe := filepath.Dir(target)
	missing := filepath.Base(target)
	for probe != target {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			return filepath.Clean(filepath.Join(resolved, missing))
		}
		missing = filepath.Join(filepath.Base(probe), missing)
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	return target
}

// relative 返回以 "/" 开头的根内相对路径，与 Legado downloadFile 返回值形式一致。
func (s *fileSandbox) relative(abs string) string {
	rel, err := filepath.Rel(s.root, abs)
	if err != nil {
		return ""
	}
	return "/" + filepath.ToSlash(rel)
}

func (s *fileSandbox) readBytes(rel string) ([]byte, bool, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// readText 读取文本文件；charsetName 为空时自动探测编码。文件不存在返回空串。
func (s *fileSandbox) readText(rel string, charsetName string) (string, error) {
	data, ok, err := s.readBytes(rel)
	if err != nil || !ok {
		return "", err
	}
	return decodeDetected(data, charsetName)
}

func decodeDetected(data []byte, charsetName string) (string, error) {
	if strings.TrimSpace(charsetName) == "" {
		_, name, _ := charset.DetermineEncoding(data, "")
		charsetName = name
	}
	return decodeCharset(data, charsetName)
}

func (s *fileSandbox) write(rel string, data []byte) (string, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		_ = os.Remove(abs)
		return "", err
	}
	return s.relative(abs), nil
}

func (s *fileSandbox) delete(rel string) (bool, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return false, err
	}
	if abs == s.root {
		return false, fmt.Errorf("%w: %s", errIllegalPath, rel)
	}
	// 目录符号链接只 unlink 链接本身，不得 RemoveAll 链接目标整棵树（S04-01）。
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(abs); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := os.RemoveAll(abs); err != nil {
		return false, err
	}
	return true, nil
}

// unzip 对齐 unArchiveFile：解压到 ArchiveTemp/<md5Encode16(文件名)>，返回该目录的相对路径。
func (s *fileSandbox) unzip(rel string) (string, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	if len(reader.File) > maxArchiveEntries {
		return "", fmt.Errorf("archive has too many entries: %d", len(reader.File))
	}
	destRel := path.Join(archiveTempFolder, md5Encode16(filepath.Base(abs)))
	dest, err := s.resolve(destRel)
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	var total int64
	for _, entry := range reader.File {
		// 拒绝 zip 内符号链接条目：解压后可被后续文件 API 读取放大逃逸面（S04-01）。
		if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: 压缩包内符号链接条目 %s", errIllegalPath, entry.Name)
		}
		target, err := s.resolve(path.Join(destRel, entry.Name))
		if err != nil {
			return "", err
		}
		if target != dest && !strings.HasPrefix(target, dest+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: %s", errIllegalPath, entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
			continue
		}
		content, err := readZipEntry(entry, maxArchiveBytes-total)
		if err != nil {
			return "", err
		}
		total += int64(len(content))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return "", err
		}
	}
	return destRel, nil
}

// readZipEntry 读取条目内容，超过 limit 字节即报错（不信任头部声明的大小）。
func readZipEntry(entry *zip.File, limit int64) ([]byte, error) {
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	content, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("archive content exceeds %d bytes", maxArchiveBytes)
	}
	return content, nil
}

// zipEntryContent 对齐 getZipByteArrayContent：返回压缩包内指定路径条目的内容，不存在时 ok=false。
func zipEntryContent(data []byte, name string) ([]byte, bool, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, false, err
	}
	for _, entry := range reader.File {
		if entry.Name != name {
			continue
		}
		content, err := readZipEntry(entry, maxArchiveBytes)
		if err != nil {
			return nil, false, err
		}
		return content, true, nil
	}
	return nil, false, nil
}

// txtInFolder 对齐 getTxtInFolder：按目录顺序读取全部文件并以换行连接，随后删除该目录。
func (s *fileSandbox) txtInFolder(rel string) (string, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(abs, entry.Name()))
		if err != nil {
			return "", err
		}
		text, err := decodeDetected(data, "")
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}
	if abs != s.root {
		if err := os.RemoveAll(abs); err != nil {
			return "", err
		}
	}
	return strings.Join(parts, "\n"), nil
}

// urlSuffix 对齐 UrlUtil.getSuffix：取路径最后一段的扩展名，非法时为 "ext"。
func urlSuffix(raw string) string {
	target := parseCustomURL(raw).URL
	if i := strings.LastIndex(target, "/"); i >= 0 {
		target = target[i+1:]
	}
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		target = target[:i]
	}
	suffix := ""
	if i := strings.LastIndex(target, "."); i >= 0 {
		suffix = target[i+1:]
	}
	if len(suffix) > 5 || !fileSuffixPattern.MatchString(suffix) {
		return "ext"
	}
	return suffix
}

// fetchBytes 以原始字节获取资源（忽略 type 选项的 hex 转换），返回 URL 选项中的 type。
func (r *Runtime) fetchBytes(runCtx evalContext, vm *goja.Runtime, raw string) ([]byte, string, error) {
	if r.policy.Fetcher == nil {
		return nil, "", runCtx.wrap(ErrScriptFailed, "fetcher is not configured")
	}
	req, err := r.buildScriptRequest(runCtx, vm, raw, nil)
	if err != nil {
		return nil, "", err
	}
	req.EncodeBodyAsHex = false
	resp, err := r.policy.Fetcher.Fetch(runCtx.ctx, req)
	if err != nil {
		return nil, "", err
	}
	fileType, _ := parseCustomURL(raw).Attribute["type"].(string)
	return resp.Body, strings.TrimSpace(fileType), nil
}

// maxHexArchiveChars 是十六进制字符串输入的最大字符数（128MB 字符 → 64MB 字节），防止超长解码一次性分配。
const maxHexArchiveChars = 128 << 20

// archiveBytes 对齐 getZipByteArrayContent 的输入：绝对 URL 时下载，否则视为十六进制字符串。
func (r *Runtime) archiveBytes(runCtx evalContext, vm *goja.Runtime, raw string) ([]byte, error) {
	if isAbsURL(raw) {
		data, _, err := r.fetchBytes(runCtx, vm, raw)
		return data, err
	}
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) > maxHexArchiveChars {
		return nil, fmt.Errorf("%w: hex input exceeds %d characters", ErrScriptFailed, maxHexArchiveChars)
	}
	return hex.DecodeString(trimmed)
}

func newFileObject(vm *goja.Runtime, sandbox *fileSandbox, abs string) *goja.Object {
	obj := vm.NewObject()
	stat := func() os.FileInfo {
		info, err := os.Stat(abs)
		if err != nil {
			return nil
		}
		return info
	}
	_ = obj.Set("exists", func() bool { return stat() != nil })
	_ = obj.Set("isFile", func() bool { info := stat(); return info != nil && !info.IsDir() })
	_ = obj.Set("isDirectory", func() bool { info := stat(); return info != nil && info.IsDir() })
	_ = obj.Set("length", func() int64 {
		if info := stat(); info != nil {
			return info.Size()
		}
		return 0
	})
	_ = obj.Set("getName", func() string { return filepath.Base(abs) })
	_ = obj.Set("getPath", func() string { return sandbox.relative(abs) })
	_ = obj.Set("toString", func() string { return sandbox.relative(abs) })
	return obj
}

// fileBindings 返回 java.* 文件与压缩包 API；7z/rar 不支持，抛 ErrScriptUnsupported。
func (r *Runtime) fileBindings(runCtx evalContext, vm *goja.Runtime) map[string]any {
	files := r.files
	must := func(err error) {
		if err != nil {
			throwJS(vm, err)
		}
	}
	zipString := func(call goja.FunctionCall) goja.Value {
		data, err := r.archiveBytes(runCtx, vm, call.Argument(0).String())
		must(err)
		content, ok, err := zipEntryContent(data, call.Argument(1).String())
		must(err)
		if !ok {
			r.log("getZipContent 未发现内容")
			return vm.ToValue("")
		}
		text, err := decodeDetected(content, optionalString(call, 2))
		must(err)
		return vm.ToValue(text)
	}
	unsupportedArchive := func(name string) func(goja.FunctionCall) goja.Value {
		return func(goja.FunctionCall) goja.Value {
			throwJS(vm, fmt.Errorf("%w: java.%s (7z/rar archives are not supported)", ErrScriptUnsupported, name))
			return goja.Undefined()
		}
	}
	bindings := map[string]any{
		"getFile": func(rel string) *goja.Object {
			abs, err := files.resolve(rel)
			must(err)
			return newFileObject(vm, files, abs)
		},
		"readFile": func(rel string) goja.Value {
			data, ok, err := files.readBytes(rel)
			must(err)
			if !ok {
				return goja.Null()
			}
			return vm.ToValue(data)
		},
		"readTxtFile": func(call goja.FunctionCall) goja.Value {
			text, err := files.readText(call.Argument(0).String(), optionalString(call, 1))
			must(err)
			return vm.ToValue(text)
		},
		"deleteFile": func(rel string) bool {
			ok, err := files.delete(rel)
			must(err)
			return ok
		},
		// downloadFile(url) 下载到 <md5Encode16(url)>.<type|后缀>；downloadFile(hex, url) 为 Legado 已废弃的十六进制写文件形式。
		"downloadFile": func(call goja.FunctionCall) goja.Value {
			if _, err := files.resolve(""); err != nil {
				throwJS(vm, err)
			}
			if len(call.Arguments) > 1 {
				content, target := call.Argument(0).String(), call.Argument(1).String()
				fileType, _ := parseCustomURL(target).Attribute["type"].(string)
				if strings.TrimSpace(fileType) == "" {
					return vm.ToValue("")
				}
				if len(content) > maxHexArchiveChars {
					throwJS(vm, fmt.Errorf("%w: hex input exceeds %d characters", ErrScriptFailed, maxHexArchiveChars))
				}
				data, err := hex.DecodeString(content)
				must(err)
				rel, err := files.write(md5Encode16(target)+"."+strings.TrimSpace(fileType), data)
				must(err)
				return vm.ToValue(rel)
			}
			target := call.Argument(0).String()
			data, fileType, err := r.fetchBytes(runCtx, vm, target)
			must(err)
			if fileType == "" {
				fileType = urlSuffix(target)
			}
			rel, err := files.write(md5Encode16(target)+"."+fileType, data)
			must(err)
			return vm.ToValue(rel)
		},
		"unzipFile": func(rel string) string {
			if rel == "" {
				return ""
			}
			out, err := files.unzip(rel)
			must(err)
			return out
		},
		"unArchiveFile": func(rel string) string {
			if rel == "" {
				return ""
			}
			if !strings.EqualFold(path.Ext(rel), ".zip") {
				throwJS(vm, fmt.Errorf("%w: java.unArchiveFile only supports zip archives", ErrScriptUnsupported))
			}
			out, err := files.unzip(rel)
			must(err)
			return out
		},
		"getTxtInFolder": func(rel string) string {
			if rel == "" {
				return ""
			}
			text, err := files.txtInFolder(rel)
			must(err)
			return text
		},
		"getZipStringContent": zipString,
		"getZipByteArrayContent": func(call goja.FunctionCall) goja.Value {
			data, err := r.archiveBytes(runCtx, vm, call.Argument(0).String())
			must(err)
			content, ok, err := zipEntryContent(data, call.Argument(1).String())
			must(err)
			if !ok {
				r.log("getZipContent 未发现内容")
				return goja.Null()
			}
			return vm.ToValue(content)
		},
	}
	for _, name := range []string{"un7zFile", "unrarFile", "getRarStringContent", "getRarByteArrayContent", "get7zStringContent", "get7zByteArrayContent"} {
		bindings[name] = unsupportedArchive(name)
	}
	return bindings
}

package script

import (
	"context"
	"fmt"
	"strings"
	"sync"

	domainsource "reader/internal/domain/source"
)

// memoryVariableRepository 是未注入持久化仓库时的进程内实现，归属单个 Runtime，不使用包级状态。
type memoryVariableRepository struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemoryVariableRepository() *memoryVariableRepository {
	return &memoryVariableRepository{values: map[string]string{}}
}

func memoryVariableKey(sourceURL string, name string) string {
	return sourceURL + "\x00" + name
}

func (m *memoryVariableRepository) GetVariable(_ context.Context, sourceURL string, name string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[memoryVariableKey(sourceURL, name)]
	return value, ok, nil
}

func (m *memoryVariableRepository) PutVariable(_ context.Context, sourceURL string, name string, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[memoryVariableKey(sourceURL, name)] = value
	return nil
}

func (m *memoryVariableRepository) DeleteVariable(_ context.Context, sourceURL string, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, memoryVariableKey(sourceURL, name))
	return nil
}

// sourceState 读写单个书源的持久化状态；读取直接查仓库，不做跨请求缓存，删除书源后不会继承旧状态。
type sourceState struct {
	ctx       context.Context
	repo      domainsource.VariableRepository
	sourceURL string
	log       func(string)
}

func (s sourceState) get(name string) string {
	value, _, err := s.repo.GetVariable(s.ctx, s.sourceURL, name)
	if err != nil {
		s.log(fmt.Sprintf("读取书源变量 %s 失败: %v", name, err))
		return ""
	}
	return value
}

// set 写入条目；空值删除条目，对齐 Legado CacheManager 对 null 的删除语义。
func (s sourceState) set(name string, value string) bool {
	var err error
	if value == "" {
		err = s.repo.DeleteVariable(s.ctx, s.sourceURL, name)
	} else {
		err = s.repo.PutVariable(s.ctx, s.sourceURL, name, value)
	}
	if err != nil {
		s.log(fmt.Sprintf("写入书源变量 %s 失败: %v", name, err))
		return false
	}
	return true
}

// SourceBindings 生成脚本可见的 source 白名单对象，对齐 Legado BaseSource 的公开方法。
// variable/loginHeader/loginInfo/put 状态写入 Runtime 的 VariableRepository；nil Runtime 时状态只存在于本对象。
func (r *Runtime) SourceBindings(ctx context.Context, src *domainsource.BookSource) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	state := sourceState{ctx: ctx, sourceURL: src.BookSourceURL, log: func(string) {}}
	if r != nil {
		state.repo = r.variables
		state.log = r.log
	} else {
		state.repo = newMemoryVariableRepository()
	}
	key := stripSourceSuffix(src.BookSourceURL)
	getLoginInfo := func() any {
		info := state.get(domainsource.VariableNameLoginInfo)
		if strings.TrimSpace(info) == "" {
			return nil
		}
		return info
	}
	return map[string]any{
		"key":               key,
		"bookSourceUrl":     src.BookSourceURL,
		"bookSourceName":    src.BookSourceName,
		"bookSourceGroup":   src.BookSourceGroup,
		"bookSourceType":    src.BookSourceType,
		"bookSourceComment": src.BookSourceComment,
		"variableComment":   src.VariableComment,
		"loginUrl":          src.LoginURL,
		"header":            src.Header,
		"jsLib":             src.JSLib,
		"enabled":           src.Enabled,
		"enabledCookieJar":  src.EnabledCookieJar,
		"getKey": func() string {
			return key
		},
		"getTag": func() string {
			return src.BookSourceName
		},
		"getLoginHeader": func() string {
			return state.get(domainsource.VariableNameLoginHeader)
		},
		"getLoginHeaderMap": func() any {
			return compatLookupMap(state.get(domainsource.VariableNameLoginHeader))
		},
		// putLoginHeader 对齐 BaseSource.kt:150-157：header 中的 Cookie 同步写入书源 Cookie。
		"putLoginHeader": func(header string) bool {
			if !state.set(domainsource.VariableNameLoginHeader, header) {
				return false
			}
			if r != nil {
				headers := parseHeaderJSONString(header)
				cookie := headers["Cookie"]
				if cookie == "" {
					cookie = headers["cookie"]
				}
				if cookie != "" {
					r.cookie.ReplaceCookie(src.BookSourceURL, cookie)
				}
			}
			return true
		},
		"removeLoginHeader": func() bool {
			return state.set(domainsource.VariableNameLoginHeader, "")
		},
		"getLoginInfo": getLoginInfo,
		"getLoginInfoMap": func() any {
			return compatLookupMap(state.get(domainsource.VariableNameLoginInfo))
		},
		"putLoginInfo": func(info string) bool {
			return state.set(domainsource.VariableNameLoginInfo, info)
		},
		"removeLoginInfo": func() bool {
			return state.set(domainsource.VariableNameLoginInfo, "")
		},
		"getVariable": func(...any) string {
			return state.get(domainsource.VariableNameSourceVariable)
		},
		"setVariable": func(value string) bool {
			return state.set(domainsource.VariableNameSourceVariable, value)
		},
		"putVariable": func(args ...any) bool {
			if len(args) != 1 {
				return false
			}
			return state.set(domainsource.VariableNameSourceVariable, variableValueString(args[0]))
		},
		"removeVariable": func() bool {
			return state.set(domainsource.VariableNameSourceVariable, "")
		},
		// put/get 对齐 BaseSource.kt:224-235：键为 v_<sourceKey>_<key>，get 缺省返回空串。
		"put": func(name string, value any) string {
			text := variableValueString(value)
			state.set(domainsource.VariableNameUserPrefix+name, text)
			return text
		},
		"get": func(name string) string {
			return state.get(domainsource.VariableNameUserPrefix + name)
		},
		"variable": state.get(domainsource.VariableNameSourceVariable),
	}
}

package sourceapp

import (
	"context"
	"encoding/json"
	"strings"

	"reader/internal/domain/source"
)

// LoginService 编排书源登录：加载书源、判定登录能力、驱动 ScriptIntegration 执行 loginUrl 脚本并读写持久化登录状态。
type LoginService struct {
	repo        source.BookSourceRepository
	integration *ScriptIntegration
}

func NewLoginService(repo source.BookSourceRepository, integration *ScriptIntegration) *LoginService {
	return &LoginService{repo: repo, integration: integration}
}

// LoginStateView 是登录状态的可序列化视图；LoginInfo/LoginHeader 为解析后的键值对，不存在时为 nil。
type LoginStateView struct {
	SourceID    string
	HasLoginURL bool
	HasLoginUI  bool
	LoggedIn    bool
	LoginInfo   map[string]string
	LoginHeader map[string]string
}

// State 返回书源当前持久化的登录状态。
func (s *LoginService) State(ctx context.Context, sourceID string) (LoginStateView, error) {
	src, err := s.repo.FindByID(ctx, sourceID)
	if err != nil {
		return LoginStateView{}, err
	}
	return s.view(ctx, src), nil
}

// Login 持久化表单值并执行 loginUrl 脚本，返回执行后的登录状态。
func (s *LoginService) Login(ctx context.Context, sourceID string, values map[string]string) (LoginStateView, error) {
	src, err := s.repo.FindByID(ctx, sourceID)
	if err != nil {
		return LoginStateView{}, err
	}
	if strings.TrimSpace(src.LoginURL) == "" {
		return LoginStateView{}, ErrLoginURLMissing
	}
	// Legado 在 loginUi 为空时打开 WebView 登录（SourceLoginActivity）；本服务不引入无头浏览器，
	// 只支持 loginUrl 自带 JS 登录流程的书源，其余明确返回不支持。
	if strings.TrimSpace(src.LoginUI) == "" {
		if _, isScript := scriptRuleText(src.LoginURL); !isScript {
			return LoginStateView{}, ErrUnsupportedLoginFlow
		}
	}
	if err := s.integration.Login(ctx, src, values); err != nil {
		return LoginStateView{}, err
	}
	return s.view(ctx, src), nil
}

// Clear 清除持久化的 loginInfo 与 loginHeader。
func (s *LoginService) Clear(ctx context.Context, sourceID string) error {
	src, err := s.repo.FindByID(ctx, sourceID)
	if err != nil {
		return err
	}
	s.integration.ClearLogin(ctx, src)
	return nil
}

func (s *LoginService) view(ctx context.Context, src *source.BookSource) LoginStateView {
	state := s.integration.LoginStateOf(ctx, src)
	return LoginStateView{
		SourceID:    src.SourceID,
		HasLoginURL: strings.TrimSpace(src.LoginURL) != "",
		HasLoginUI:  strings.TrimSpace(src.LoginUI) != "",
		LoggedIn:    strings.TrimSpace(state.Header) != "",
		LoginInfo:   parseLoginMap(state.Info),
		LoginHeader: parseLoginMap(state.Header),
	}
}

func parseLoginMap(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

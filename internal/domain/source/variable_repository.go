package source

import "context"

// 书源级脚本状态的固定条目名，对齐 Legado BaseSource 在 CacheManager 中保存的键。
const (
	VariableNameSourceVariable = "sourceVariable" // source.setVariable/getVariable
	VariableNameLoginHeader    = "loginHeader"    // source.putLoginHeader/getLoginHeader
	VariableNameLoginInfo      = "loginInfo"      // source.putLoginInfo/getLoginInfo
	VariableNameUserPrefix     = "v_"             // source.put(key)/get(key) 的键前缀
)

// VariableRepository 持久化书源级脚本状态（source_variables 表）。
// sourceURL 使用书源原始 BookSourceURL（Legado getKey），与删除书源时的清理键一致。
type VariableRepository interface {
	// GetVariable 读取条目；不存在时 ok=false 且 err=nil。
	GetVariable(ctx context.Context, sourceURL string, name string) (value string, ok bool, err error)
	// PutVariable 写入或覆盖条目。
	PutVariable(ctx context.Context, sourceURL string, name string, value string) error
	// DeleteVariable 删除条目；条目不存在不算错误。
	DeleteVariable(ctx context.Context, sourceURL string, name string) error
}

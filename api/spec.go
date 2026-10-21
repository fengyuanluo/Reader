// Package apispec 内嵌仓库唯一的 OpenAPI 规范真相源 api/openapi.yaml，
// 供 handler 与其他包通过 embed 引用，避免维护第二份拷贝（S18-09）。
package apispec

import "embed"

//go:embed openapi.yaml
var Spec embed.FS

// OpenAPIYAML 是内嵌规范内文件的路径，调用方以 Spec.ReadFile(OpenAPIYAML) 读取。
const OpenAPIYAML = "openapi.yaml"

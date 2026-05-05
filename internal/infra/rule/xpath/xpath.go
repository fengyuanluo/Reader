package xpath

import (
	"errors"
	"fmt"
	"strings"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"

	"reader/internal/infra/rule/parser"
)

var ErrRuleParse = errors.New("rule_parse_failed")

type Adapter struct {
	doc *html.Node
}

func New(input string) (*Adapter, error) {
	doc, err := htmlquery.Parse(strings.NewReader(input))
	if err != nil {
		return nil, err
	}
	return &Adapter{doc: doc}, nil
}

func (a *Adapter) Nodes(rule string) ([]*html.Node, error) {
	return selectNodes(a.doc, rule)
}

func (a *Adapter) Strings(rule string) ([]string, error) {
	chain, err := parser.Split(strings.TrimSpace(rule), "&&", "||", "%%")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRuleParse, err)
	}
	results := [][]string{}
	for _, step := range chain.Steps {
		values, err := a.stringStep(step.Raw)
		if err != nil {
			return nil, err
		}
		if len(values) > 0 {
			results = append(results, values)
			if chain.Separator == "||" {
				break
			}
		}
	}
	if chain.Separator == "%%" {
		return interleave(results), nil
	}
	out := []string{}
	for _, values := range results {
		out = append(out, values...)
	}
	return out, nil
}

func (a *Adapter) String(rule string) (string, error) {
	values, err := a.Strings(rule)
	if err != nil {
		return "", err
	}
	return strings.Join(values, "\n"), nil
}

func (a *Adapter) stringStep(rule string) ([]string, error) {
	nodes, err := selectNodes(a.doc, rule)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, node := range nodes {
		value := strings.TrimSpace(htmlquery.OutputHTML(node, false))
		if node.Type == html.TextNode || node.FirstChild == nil {
			value = strings.TrimSpace(htmlquery.InnerText(node))
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return out, nil
}

func selectNodes(root *html.Node, rule string) ([]*html.Node, error) {
	rule = strings.TrimSpace(strings.TrimPrefix(rule, "@XPath:"))
	if rule == "" {
		return nil, fmt.Errorf("%w: empty xpath", ErrRuleParse)
	}
	chain, err := parser.Split(rule, "&&", "||", "%%")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRuleParse, err)
	}
	out := []*html.Node{}
	for _, step := range chain.Steps {
		nodes, err := htmlquery.QueryAll(root, step.Raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrRuleParse, err)
		}
		if len(nodes) > 0 {
			out = append(out, nodes...)
			if chain.Separator == "||" {
				break
			}
		}
	}
	return out, nil
}

func interleave(results [][]string) []string {
	if len(results) == 0 {
		return nil
	}
	out := []string{}
	for i := range results[0] {
		for _, values := range results {
			if i < len(values) {
				out = append(out, values[i])
			}
		}
	}
	return out
}

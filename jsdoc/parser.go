package jsdoc

import (
	"regexp"
	"strings"
)

type Annotation struct {
	Tag   string
	Value string
}

type Comment struct {
	Annotations []Annotation
}

var tagRe = regexp.MustCompile(`@(\w+)\s*(?:\{([^}]+)\})?([^\n@]*)`)

func Parse(raw string) *Comment {
	c := &Comment{}
	for _, m := range tagRe.FindAllStringSubmatch(raw, -1) {
		tag := strings.TrimSpace(m[1])
		val := strings.TrimSpace(m[2])
		if val == "" {
			val = strings.TrimSpace(m[3])
		}
		c.Annotations = append(c.Annotations, Annotation{Tag: tag, Value: val})
	}
	return c
}

func (c *Comment) GetType() string {
	for _, a := range c.Annotations {
		if a.Tag == "type" {
			return a.Value
		}
	}
	return ""
}

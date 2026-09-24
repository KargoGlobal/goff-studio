package goff

import "strings"

type Operator struct {
	Key   string
	Label string
	Arity int
}

var Operators = []Operator{
	{"eq", "equals", 1},
	{"ne", "does not equal", 1},
	{"co", "contains", 1},
	{"sw", "starts with", 1},
	{"ew", "ends with", 1},
	{"in", "is one of", 2},
	{"notin", "is not one of", 2},
	{"gt", "is greater than", 1},
	{"ge", "is greater than or equal to", 1},
	{"lt", "is less than", 1},
	{"le", "is less than or equal to", 1},
	{"pr", "is present", 0},
}

var byKey = func() map[string]Operator {
	m := map[string]Operator{}
	for _, op := range Operators {
		m[op.Key] = op
	}
	return m
}()

func LookupOperator(key string) (Operator, bool) {
	op, ok := byKey[strings.ToLower(key)]
	return op, ok
}

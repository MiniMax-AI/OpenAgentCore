package api

import v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"

func listBounds[T any](data []T, id func(T) string) (*string, *string) {
	if len(data) == 0 {
		return nil, nil
	}
	first, last := id(data[0]), id(data[len(data)-1])
	return &first, &last
}

func sessionListResponse(data []v1.Session, more bool) v1.SessionList {
	if data == nil {
		data = []v1.Session{}
	}
	first, last := listBounds(data, func(value v1.Session) string { return value.ID })
	return v1.SessionList{Object: "list", Data: data, HasMore: more, FirstID: first, LastID: last}
}

func turnListResponse(data []v1.Turn, more bool) v1.TurnList {
	if data == nil {
		data = []v1.Turn{}
	}
	first, last := listBounds(data, func(value v1.Turn) string { return value.ID })
	return v1.TurnList{Object: "list", Data: data, HasMore: more, FirstID: first, LastID: last}
}

func itemListResponse(data []v1.Item, more bool) v1.ItemList {
	if data == nil {
		data = []v1.Item{}
	}
	first, last := listBounds(data, func(value v1.Item) string { return value.ID })
	return v1.ItemList{Object: "list", Data: data, HasMore: more, FirstID: first, LastID: last}
}

func subagentListResponse(data []v1.Subagent, more bool) v1.SubagentList {
	if data == nil {
		data = []v1.Subagent{}
	}
	first, last := listBounds(data, func(value v1.Subagent) string { return value.ID })
	return v1.SubagentList{Object: "list", Data: data, HasMore: more, FirstID: first, LastID: last}
}

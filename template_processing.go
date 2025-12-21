package structure_websites

import "go.mongodb.org/mongo-driver/bson/primitive"

func sanitizeTemplateDocument(doc map[string]interface{}) map[string]interface{} {
	if doc == nil {
		return nil
	}
	template := buildTemplateValue(doc)
	if mapped, ok := template.(map[string]interface{}); ok {
		return mapped
	}
	return make(map[string]interface{})
}

func buildTemplateValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, raw := range v {
			result[key] = mergeTemplateValues(result[key], buildTemplateValue(raw))
		}
		return result
	case primitive.M:
		return buildTemplateValue(map[string]interface{}(v))
	case []interface{}:
		var element interface{}
		for _, item := range v {
			element = mergeTemplateValues(element, buildTemplateValue(item))
		}
		if element == nil {
			return []interface{}{}
		}
		return []interface{}{element}
	case primitive.A:
		generic := make([]interface{}, len(v))
		for i, item := range v {
			generic[i] = item
		}
		return buildTemplateValue(generic)
	case string, nil:
		return ""
	case bool:
		return false
	case int, int32, int64:
		return 0
	case float32, float64:
		return 0
	default:
		return ""
	}
}

func mergeTemplateValues(base, incoming interface{}) interface{} {
	if base == nil {
		return copyTemplateValue(incoming)
	}
	if incoming == nil {
		return base
	}

	switch b := base.(type) {
	case map[string]interface{}:
		incomingMap := toTemplateMap(incoming)
		for key, val := range incomingMap {
			b[key] = mergeTemplateValues(b[key], val)
		}
		return b
	case []interface{}:
		incomingSlice := toTemplateSlice(incoming)
		if len(b) == 0 && len(incomingSlice) == 0 {
			return []interface{}{}
		}
		var baseElem interface{}
		if len(b) > 0 {
			baseElem = b[0]
		}
		var incomingElem interface{}
		if len(incomingSlice) > 0 {
			incomingElem = incomingSlice[0]
		}
		merged := mergeTemplateValues(baseElem, incomingElem)
		if merged == nil {
			return []interface{}{}
		}
		return []interface{}{merged}
	default:
		return base
	}
}

func copyTemplateValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		cloned := make(map[string]interface{}, len(v))
		for key, val := range v {
			cloned[key] = copyTemplateValue(val)
		}
		return cloned
	case []interface{}:
		cloned := make([]interface{}, len(v))
		for i, item := range v {
			cloned[i] = copyTemplateValue(item)
		}
		return cloned
	default:
		return v
	}
}

func toTemplateMap(value interface{}) map[string]interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return v
	case []interface{}:
		if len(v) == 0 {
			return make(map[string]interface{})
		}
		if first, ok := v[0].(map[string]interface{}); ok {
			return first
		}
	case nil:
		return make(map[string]interface{})
	}
	return make(map[string]interface{})
}

func toTemplateSlice(value interface{}) []interface{} {
	switch v := value.(type) {
	case []interface{}:
		return v
	case map[string]interface{}:
		return []interface{}{v}
	case nil:
		return []interface{}{}
	default:
		return []interface{}{copyTemplateValue(value)}
	}
}

func cloneMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(src))
	for key, value := range src {
		cloned[key] = deepCopyValue(value)
	}
	return cloned
}

func deepCopyValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return cloneMap(v)
	case []interface{}:
		copied := make([]interface{}, len(v))
		for i, item := range v {
			copied[i] = deepCopyValue(item)
		}
		return copied
	case primitive.M:
		return cloneMap(map[string]interface{}(v))
	case primitive.A:
		copied := make([]interface{}, len(v))
		for i, item := range v {
			copied[i] = deepCopyValue(item)
		}
		return copied
	default:
		return v
	}
}

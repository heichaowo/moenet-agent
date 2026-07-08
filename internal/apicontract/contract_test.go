package apicontract_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/moenet/moenet-agent/internal/api"
	"github.com/moenet/moenet-agent/internal/apicontract/agentapi"
	"github.com/moenet/moenet-agent/internal/apicontract/cpapi"
	"github.com/moenet/moenet-agent/internal/community"
	"github.com/moenet/moenet-agent/internal/probe"
	"github.com/moenet/moenet-agent/internal/task"
)

// jsonKind reduces a Go type to its coarse JSON wire kind, so shapes are
// compared at the wire level: map[int]int and map[string]int are both objects,
// *string and string are both strings, uint32 and int are both numbers.
func jsonKind(t reflect.Type) string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct, reflect.Interface:
		return "object"
	default:
		return t.Kind().String()
	}
}

// jsonFields maps json tag name -> wire kind for a struct type.
func jsonFields(t reflect.Type) map[string]string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	out := map[string]string{}
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = jsonKind(t.Field(i).Type)
	}
	return out
}

// assertMatchesContract fails if the hand-written struct is missing a field the
// contract declares, or if a shared field's JSON wire kind differs. This is the
// class of drift that broke bird-config sync fleet-wide (dn42As string vs number).
func assertMatchesContract(t *testing.T, name string, contract, local any) {
	t.Helper()
	c := jsonFields(reflect.TypeOf(contract))
	l := jsonFields(reflect.TypeOf(local))
	for field, ckind := range c {
		lkind, ok := l[field]
		if !ok {
			t.Errorf("%s: hand-written struct is missing contract field %q", name, field)
			continue
		}
		if lkind != ckind {
			t.Errorf("%s: field %q wire-kind drift — contract=%s hand-written=%s", name, field, ckind, lkind)
		}
	}
}

func TestHandwrittenTypesMatchContract(t *testing.T) {
	assertMatchesContract(t, "BirdPolicy", cpapi.BirdPolicy{}, task.BirdPolicy{})
	assertMatchesContract(t, "ToolResponse", agentapi.ToolResponse{}, api.ToolResponse{})
	assertMatchesContract(t, "CommunityStats", agentapi.CommunityStats{}, community.CommunityStats{})
	assertMatchesContract(t, "ProbeResult", agentapi.ProbeResult{}, probe.ProbeResult{})
	assertMatchesContract(t, "PeerStats", agentapi.PeerStats{}, probe.PeerStats{})
}

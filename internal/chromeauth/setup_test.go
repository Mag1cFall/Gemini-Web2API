package chromeauth

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// TestPromptProfiles 验证交互编号映射与去重
func TestPromptProfiles(t *testing.T) {
	accounts := []Account{
		{Profile: "Profile 1", Email: "one@example.com", Importable: true},
		{Profile: "Profile 2", Email: "two@example.com", Importable: false},
		{Profile: "Profile 3", Email: "three@example.com", Importable: true},
	}
	var output bytes.Buffer
	selected, err := promptProfiles(accounts, strings.NewReader("2,1,2\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	want := stringList{"Profile 3", "Profile 1"}
	if !reflect.DeepEqual(selected, want) {
		t.Fatalf("selected = %#v, want %#v", selected, want)
	}
}

// TestPromptProfilesRejectsEmpty 验证交互模式不会默认全选
func TestPromptProfilesRejectsEmpty(t *testing.T) {
	accounts := []Account{{Profile: "Profile 1", Importable: true}}
	_, err := promptProfiles(accounts, strings.NewReader("\n"), &bytes.Buffer{})
	if err == nil {
		t.Fatal("empty selection should fail")
	}
}

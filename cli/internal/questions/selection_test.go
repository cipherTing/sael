package questions

import "testing"

func TestSelectSendsOnlyRequestedModerationQuestions(t *testing.T) {
	selected, err := Select([]string{"gore", "self_harm"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected["gore"].QuestionType() != "score" || selected["self_harm"].QuestionType() != "noul" {
		t.Fatalf("wrong question selection: %#v", selected)
	}
	if _, found := selected["sexual"]; found {
		t.Fatal("unrequested question was included")
	}
}

func TestSelectRejectsEmptyUnknownAndDuplicateQuestions(t *testing.T) {
	for _, keys := range [][]string{nil, {}, {"missing"}, {"gore", "gore"}} {
		if _, err := Select(keys); err == nil {
			t.Fatalf("accepted invalid selection: %v", keys)
		}
	}
}

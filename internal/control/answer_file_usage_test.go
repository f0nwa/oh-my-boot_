package control

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestAnswerFileUsageFrozenBindings(t *testing.T) {
	items := []Installation{{ID: 1, Title: "Windows current", AnswerID: 10}, {ID: 2, Title: "Drivers", DriverConfigID: 7}, {ID: 3, Title: "Ubuntu current", AutoinstallID: 20}}
	configs := []DriverConfig{{ID: 7, AnswerID: 11}, {ID: 8, AnswerID: 12}}
	active := MenuDraft{Items: []MenuDraftItem{
		{InstallationID: 1, Components: &InstallationComponents{AnswerID: 9}},
		{InstallationID: 2, Components: &InstallationComponents{DriverConfigID: 8}},
		{InstallationID: 3, Components: &InstallationComponents{AutoinstallID: 19}},
		{InstallationID: 99, Components: &InstallationComponents{AutoinstallID: 19}},
	}}
	got := buildAnswerFileUsage(items, configs, active)
	if !reflect.DeepEqual(got.Windows[10].Downloads, []answerFileDownload{{ID: 1, Title: "Windows current"}}) || got.Windows[10].Published != 0 {
		t.Fatalf("current windows=%+v", got.Windows[10])
	}
	if got.Windows[9].Published != 1 || got.Windows[12].Published != 1 || got.Windows[11].Published != 0 {
		t.Fatalf("frozen windows=%+v", got.Windows)
	}
	if got.Ubuntu[19].Published != 2 || len(got.Ubuntu[19].Downloads) != 0 || len(got.Ubuntu[20].Downloads) != 1 {
		t.Fatalf("frozen ubuntu=%+v", got.Ubuntu)
	}
}

type answerUsageFailureStore struct {
	*draftStore
	itemsErr, configsErr, menuErr error
}

func (s answerUsageFailureStore) Installations(context.Context) ([]Installation, error) {
	return nil, s.itemsErr
}
func (s answerUsageFailureStore) DriverConfigs(context.Context) ([]DriverConfig, error) {
	return nil, s.configsErr
}
func (s answerUsageFailureStore) MenuVersions(context.Context) ([]MenuVersion, error) {
	return nil, s.menuErr
}
func TestAnswerFileUsageErrorsAreNotZeroUses(t *testing.T) {
	broken := errors.New("database unavailable")
	for _, tc := range []struct {
		name                      string
		items, configs, menu      error
		downloadsKnown, menuKnown bool
	}{
		{"empty", nil, nil, nil, true, true}, {"downloads", broken, nil, nil, false, false}, {"drivers", nil, broken, nil, false, false}, {"menu", nil, nil, broken, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Server{Store: answerUsageFailureStore{draftStore: newDraftStore(t, "admin", nil), itemsErr: tc.items, configsErr: tc.configs, menuErr: tc.menu}}
			got := s.loadAnswerFileUsage(context.Background())
			if got.DownloadsKnown != tc.downloadsKnown || got.MenuKnown != tc.menuKnown {
				t.Fatalf("known flags=%+v", got)
			}
		})
	}
}

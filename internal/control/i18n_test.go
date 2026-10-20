package control

import "testing"

func TestTranslateMessages(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Меню опубликовано", "Menu published"},
		{"Сначала удалите конфигурацию драйверов № 3", "Delete driver configuration #3 first"},
		{"Профиль Windows ISO недоступен: хранилище «DSM» недоступно: lstat /mnt/x: host is down", "Windows ISO profile is unavailable: storage “DSM” is unavailable: lstat /mnt/x: host is down"},
		{"Хранилище «DSM» недоступно: не отвечает дольше 20s; обновите каталог", "Storage “DSM” is unavailable: not responding for more than 20s; refresh the catalog"},
		{"Хранилище недоступно: не отвечает дольше 20s", "Storage is unavailable: not responding for more than 20s"},
		{"Работает · abc123", "Running · abc123"},
		{"Работает · full · ens19", "Running · full · ens19"},
		{"DSM", "DSM"},
	} {
		if got := translate(langEN, tc.in); got != tc.want {
			t.Errorf("translate(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := translate(langRU, tc.in); got != tc.in {
			t.Errorf("Russian changed %q to %q", tc.in, got)
		}
	}
}

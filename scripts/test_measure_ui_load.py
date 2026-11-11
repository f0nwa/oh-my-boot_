"""Guard against false successful load reports (auth redirects, lost rows)."""
import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("load", Path(__file__).with_name("measure-ui-load.py"))
load = importlib.util.module_from_spec(spec)
spec.loader.exec_module(load)


class LoadValidationTests(unittest.TestCase):
    def test_redirect_is_failure_even_with_fast_response(self):
        self.assertFalse(load.success(302, b""))
        self.assertFalse(load.success(200, b'<form action="/login">login</form>'))

    def test_source_error_in_200_fragment_is_failure(self):
        body = '<p class="error">Журнал временно недоступен</p><table><tbody></tbody></table>'.encode()
        self.assertFalse(load.success(200, body, "/events/content?tab=requests"))
        self.assertFalse(load.success(200, b'<p>no table</p>', "/events/content?tab=dhcp"))
        self.assertTrue(load.success(200, b'<table><tbody><tr><td>event</td></tr></tbody></table>',
                                     "/events/content?tab=requests"))

    def test_card_source_error_is_failure(self):
        self.assertFalse(load.success(200, b'<h1>load-0001</h1><div class="events-error-block">error</div>',
                                      "/events/machines/02:7e:57:00:00:01"))

    def test_empty_partial_cannot_replace_expected_rows(self):
        self.assertFalse(load.success(200, b'<div class="infinite-scroll-sentinel"></div>',
                                      "/machines?partial=1&offset=50", expected_rows=50))

    def test_rows_and_cursor_are_read_from_partial_fragment(self):
        p = load.parse_page('<tr><td>/load/000001</td></tr><div class="infinite-scroll-sentinel" '
                            'data-url="/events/content?tab=requests&amp;mac=x" data-before-id="42" '
                            'data-before-time="2026-11-11T00:00:00Z" data-has-more="true"></div>')
        self.assertEqual(p.rows, [["/load/000001"]])
        self.assertIn("before_id=42", load.next_path(p.sentinel))
        self.assertIn("mac=x", load.next_path(p.sentinel))

    def test_duplicate_or_missing_rows_cannot_pass(self):
        with self.assertRaises(AssertionError):
            load.check_ids(["a", "a"], {"a", "b"})
        with self.assertRaises(AssertionError):
            load.check_ids(["a"], {"a", "b"})
        load.check_ids(["b", "a"], {"a", "b"})

    def test_exited_or_replaced_process_is_failure(self):
        samples = [{"units": {"control": {"pid": 123, "restarts": 0}}},
                   {"units": {"control": {"pid": 0, "restarts": 0}}}]
        with self.assertRaises(AssertionError):
            load.check_processes(samples)


if __name__ == "__main__":
    unittest.main()

"""Regression cases for historical deletion, renamed policy, and missing refs."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('guard', Path(__file__).with_name('check-public-history.py'))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class HistoryGuardTest(unittest.TestCase):
    def setUp(self):
        self.previous = Path.cwd()
        self.temp = tempfile.TemporaryDirectory()
        os.chdir(self.temp.name)
        self.git('init', '-q')
        self.git('config', 'user.name', 'History test')
        self.git('config', 'user.email', 'history@example.invalid')

    def tearDown(self):
        os.chdir(self.previous)
        self.temp.cleanup()

    def git(self, *args):
        return subprocess.check_output(['git', *args], stderr=subprocess.DEVNULL, text=True)

    def commit(self, path, value):
        file = Path(path)
        file.parent.mkdir(parents=True, exist_ok=True)
        file.write_text(value)
        self.git('add', '.')
        self.git('commit', '-qm', 'Fixture')

    def test_deleted_private_path_is_still_blocked(self):
        self.commit('server/internal/billing/charge.go', 'package billing\n')
        self.git('rm', '-q', 'server/internal/billing/charge.go')
        self.git('commit', '-qm', 'Delete from tip')
        self.assertTrue(guard.audit(['HEAD'])[2])

    def test_policy_cannot_hide_at_another_path(self):
        self.commit('misc/unrelated.txt', 'const ' + 'Microusd' + 'PerCredit = 10\n')
        self.assertTrue(guard.audit(['HEAD'])[2])

    def test_neutral_adapter_and_customer_results_are_public(self):
        self.commit('server/internal/billingadapter/api.go', 'package billingadapter\n// admission, settlement, available usage\n')
        self.assertFalse(guard.audit(['HEAD'])[2])
        with self.assertRaises(subprocess.CalledProcessError):
            guard.audit(['missing-branch'])


if __name__ == '__main__':
    unittest.main()

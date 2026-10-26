import argparse
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('live_eval', Path(__file__).with_name('live_eval.py'))
eval_tool = importlib.util.module_from_spec(spec)
spec.loader.exec_module(eval_tool)

class LiveEvalSafetyTests(unittest.TestCase):
    def args(self, **values):
        data = dict(kind='analysis', provider='openai-text', model='synthetic-model', budget_micros=100, reserve_micros=60, currency='USD', timeout=10, base_url='http://127.0.0.1:8080')
        data.update(values)
        return argparse.Namespace(**data)

    def test_unknown_reservation_is_not_spendable_after_restart(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'ledger.json'
            eval_tool.reserve(path, self.args(), 'first')
            with self.assertRaises(ValueError):
                eval_tool.reserve(path, self.args(), 'second')
            self.assertEqual(json.loads(path.read_text())['reservations'][0]['state'], 'unknown')

    def test_validation_blocks_cross_currency_overbudget_and_remote(self):
        for override in ({'reserve_micros': 101}, {'reserve_micros': -1}, {'base_url': 'http://localhost.evil:8080'}, {'base_url': 'http://127.0.0.1@evil'}, {'base_url': 'https://example.com'}, {'model': '../x?api_key=secret'}, {'provider': 'jimeng-images'}):
            with self.subTest(override=override), self.assertRaises(ValueError):
                eval_tool.validate(self.args(**override))
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'ledger.json'
            eval_tool.reserve(path, self.args(), 'one')
            with self.assertRaises(ValueError):
                eval_tool.reserve(path, self.args(currency='CNY'), 'two')

    def test_dry_run_never_reads_stdin_or_opens_network(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(eval_tool.urllib.request, 'build_opener', side_effect=AssertionError('network')), patch.object(eval_tool.sys, 'stdin') as stdin:
            stdin.read.side_effect = AssertionError('credential read')
            report = Path(folder) / 'report.json'
            code = eval_tool.main(['--kind', 'analysis', '--provider', 'openai-text', '--model', 'synthetic-model', '--budget-micros', '100', '--reserve-micros', '100', '--currency', 'USD', '--report', str(report)])
            self.assertEqual(code, 0)
            self.assertEqual(json.loads(report.read_text())['cost_state'], 'not_called')

    def test_live_is_disabled_even_with_authorization(self):
        with tempfile.TemporaryDirectory() as folder, patch.object(eval_tool, 'run', side_effect=AssertionError('live called')):
            report = Path(folder) / 'report.json'
            report.write_text('{}')
            with self.assertRaises(SystemExit) as result:
                eval_tool.main(['--kind', 'analysis', '--provider', 'openai-text', '--model', 'synthetic-model', '--budget-micros', '100', '--reserve-micros', '100', '--currency', 'USD', '--report', str(report), '--execute-live', '--authorize-provider-model-budget'])
            self.assertEqual(result.exception.code, 2)
            self.assertEqual(report.read_text(), '{}')

    def test_approved_caps_and_retired_video(self):
        for override in ({'budget_micros': 3000001}, {'currency': 'CNY', 'budget_micros': 5000001}, {'currency': 'EUR'}, {'kind': 'video', 'provider': 'openai-video'}):
            with self.subTest(override=override), self.assertRaises(ValueError):
                eval_tool.validate(self.args(**override))

    def test_candidate_contract_inventory_cannot_claim_executable(self):
        plan = json.loads((Path(__file__).resolve().parents[1] / 'eval/candidate-plan.json').read_text())
        self.assertFalse(plan['live_authorized'])
        self.assertEqual({b['currency']: b['approved_minor'] for b in plan['budgets']}, {'USD': 300, 'CNY': 500})
        for case in plan['cases']:
            self.assertFalse(case['harness_supported'])
            self.assertIn('managed_eval_harness_not_implemented', case['execution_blockers'])
        for case in plan['cases'][:2]:
            self.assertEqual(case['requested_specification']['max_output_tokens_including_thinking'], 500)
            self.assertEqual(case['adapter_contract']['maximum_output_parameter'], 6000)

    def test_missing_authorization_never_reads_session(self):
        with patch.object(eval_tool.sys, 'stdin') as stdin:
            stdin.read.side_effect = AssertionError('credential read')
            with self.assertRaises(SystemExit) as result:
                eval_tool.main(['--kind', 'analysis', '--provider', 'openai-text', '--model', 'synthetic-model', '--budget-micros', '100', '--reserve-micros', '100', '--currency', 'USD', '--report', '/tmp/not-created.json', '--execute-live'])
            self.assertEqual(result.exception.code, 2)

if __name__ == '__main__':
    unittest.main()

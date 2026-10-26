import argparse
import importlib.util
import json
import copy
import datetime
import io
import os
import urllib.parse
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

    def test_budget_authorization_does_not_authorize_session_transmission(self):
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
            self.assertEqual(case['harness_supported'], not bool(case['execution_blockers']))
        self.assertFalse(next(c for c in plan['cases'] if c['id'] == 'case-09')['harness_supported'])
        self.assertTrue(next(c for c in plan['cases'] if c['id'] == 'case-14')['harness_supported'])
        for case in plan['cases'][:2]:
            self.assertEqual(case['requested_specification']['max_output_tokens_including_thinking'], 500)
            self.assertEqual(case['adapter_contract']['maximum_output_parameter'], 6000)

    def test_missing_authorization_never_reads_session(self):
        with patch.object(eval_tool.sys, 'stdin') as stdin:
            stdin.read.side_effect = AssertionError('credential read')
            with self.assertRaises(SystemExit) as result:
                eval_tool.main(['--kind', 'analysis', '--provider', 'openai-text', '--model', 'synthetic-model', '--budget-micros', '100', '--reserve-micros', '100', '--currency', 'USD', '--report', '/tmp/not-created.json', '--execute-live'])
            self.assertEqual(result.exception.code, 2)



class FakeResponse:
    def __init__(self, value): self.raw = json.dumps(value).encode()
    def __enter__(self): return self
    def __exit__(self, *args): return False
    def read(self, maximum): return self.raw[:maximum]


class ManagedEvalTests(unittest.TestCase):
    def setUp(self):
        self.folder = tempfile.TemporaryDirectory()
        self.addCleanup(self.folder.cleanup)
        self.args = argparse.Namespace(kind='analysis', provider='openai-text', model='gpt-6.1-sol',
            case_id='case-01', model_config_id='config-synthetic', model_config_version=3, currency='USD',
            budget_micros=3000000, reserve_micros=100000, timeout=3, base_url='http://127.0.0.1:8080',
            origin='http://localhost:5173', voice='', scenario='original', authorize_provider_model_budget=True,
            authorize_session_transmission=True, ledger=Path(self.folder.name)/'ledger.json')
        self.auth = {'cookie': 'studio_session=synthetic-secret', 'csrf_token': 'synthetic-csrf', 'space_id': 'tenant-synthetic'}
        self.report = {'run_id': 'eval-synthetic', 'cost_state': 'not_called'}
        self.requests = []
        self.quote = {'currency': 'USD', 'minor_units': 2, 'maximum_minor': 10, 'source': 'synthetic-review',
            'expires_at': (datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(minutes=5)).isoformat(),
            'contract': 'studio-request-v1', 'request_sha256': 'a'*64,
            'model_config_id': 'config-synthetic', 'model_config_version': 3}
        self.budget = {'currency':'USD', 'minor_units':2, 'limit_minor':300, 'exposure_minor':0, 'available_minor':300}
        self.catalog_change = None
        self.quote_change = None
        self.submit_error = None
        self.catalog_calls = 0
        self.job_status = 'succeeded'

    def open(self, request, **kwargs):
        path = urllib.parse.urlsplit(request.full_url).path
        self.requests.append(request)
        if path.endswith('/auth/session'):
            return FakeResponse({'mode':'secure', 'authenticated':True, 'spaces':[{'id':'tenant-synthetic'}]})
        if path.endswith('/runtime-models'):
            self.catalog_calls += 1
            item={'id':self.args.model_config_id, 'version':3, 'provider':self.args.provider,
                  'model':self.args.model, 'capability':'text' if self.args.kind=='analysis' else 'asr' if self.args.kind=='transcription' else self.args.kind, 'available':True}
            if self.catalog_change: self.catalog_change(item, self.catalog_calls)
            return FakeResponse({'governance_ready':True, 'models':[item]})
        if path.endswith('/governance'): return FakeResponse(self.budget)
        if path.endswith('/projects'): return FakeResponse({'id':'project-synthetic', 'version':1})
        if path.endswith('/uploads'): return FakeResponse({'assets':[{'id':'asset-synthetic','kind':'image'}]})
        if path.endswith('/demo'): return FakeResponse({'version':2, 'assets':[{'id':'audio-synthetic','kind':'audio','duration':2}], 'timeline':{'clips':[{'id':'clip-synthetic'}]}})
        if path.endswith('/timeline'): return FakeResponse({'version':3})
        if path.endswith('/alignments'): return FakeResponse({'id':'transcript-synthetic','status':'succeeded','provider':self.args.provider,'model':self.args.model,'mode':'transcription','timing_basis':'transcript-only','cues':[],'transcript':'Synthetic transcript.'})
        if path.endswith('/runtime-quote'):
            if self.quote_change: self.quote_change(self.quote)
            return FakeResponse(self.quote)
        if path.endswith('/creative-jobs'):
            if self.submit_error: raise self.submit_error
            return FakeResponse({'id':'job-synthetic', 'status':self.job_status, 'provider':self.args.provider,
                                 'model':self.args.model, 'analysis':{'summary':'synthetic'}, 'asset_id':'asset-result'})
        raise AssertionError('unexpected route '+path)

    def execute(self):
        with patch.object(eval_tool.urllib.request, 'build_opener', return_value=self):
            return eval_tool.run(self.args, self.auth, self.report)

    def posts(self):
        return [r for r in self.requests if r.data is not None and urllib.parse.urlsplit(r.full_url).path.endswith('/creative-jobs')]

    def test_managed_quote_budget_and_single_submission(self):
        self.assertEqual(self.execute(), 0)
        self.assertEqual(len(self.posts()), 1)
        body=json.loads(self.posts()[0].data)
        self.assertEqual(body['model_config_id'],'config-synthetic')
        self.assertEqual(body['model_config_version'],3)
        self.assertEqual(body['quote_request_sha256'],'a'*64)
        self.assertEqual(body['budget_maximum_minor'],10)
        self.assertNotIn('expected_provider',body)
        self.assertEqual(self.report['cost_state'],'unknown_reserved')
        self.assertEqual(json.loads(self.args.ledger.read_text())['reservations'][0]['state'],'unknown')
        self.assertNotIn('synthetic-secret',json.dumps(self.report))

    def test_reference_is_synthetic_png_and_bound_to_quote(self):
        self.args.kind='image';self.args.provider='gemini-interactions-images';self.args.model='gemini-nano-banana-2.1';self.args.case_id='case-07';self.args.scenario='reference'
        self.assertEqual(self.execute(),0)
        upload=next(r for r in self.requests if '/uploads?' in r.full_url)
        self.assertTrue(upload.data.startswith(b'\x89PNG\r\n\x1a\n'))
        self.assertEqual(upload.get_header('Content-type'),'image/png')
        quote=json.loads(next(r for r in self.requests if '/runtime-quote?' in r.full_url).data)
        self.assertEqual(quote['input']['reference_asset_id'],'asset-synthetic')
        self.assertEqual(json.loads(self.posts()[0].data)['reference_asset_id'],'asset-synthetic')

    def test_speech_explicit_voice_and_200_char_bound(self):
        self.args.kind='speech';self.args.provider='minimax-speech';self.args.model='speech-2.8-turbo';self.args.case_id='case-13';self.args.voice='synthetic-voice'
        self.assertEqual(self.execute(),0)
        body=json.loads(self.posts()[0].data)
        self.assertEqual(body['voice'],'synthetic-voice')
        self.assertLessEqual(len(body['prompt']),200)

    def test_text_transcription_uses_alignment_quote_and_no_subtitle_cues(self):
        self.args.case_id='case-14'; self.args.kind='transcription'; self.args.provider='openai-asr'; self.args.model='gpt-transcribe'
        self.assertEqual(self.execute(),0)
        quote=next(json.loads(r.data) for r in self.requests if urllib.parse.urlsplit(r.full_url).path.endswith('/runtime-quote'))
        self.assertEqual(quote['operation'],'alignment')
        self.assertEqual(quote['input']['mode'],'transcription')
        self.assertNotIn('text',quote['input'])
        self.assertFalse(self.posts())
        self.assertTrue(self.report['checks']['text_only'])
        self.assertIn('not speech quality',self.report['audio_fixture'])

    def test_missing_or_incompatible_case_denies_before_network(self):
        for case,kind,provider,model in [('case-14','asr','openai-asr','gpt-transcribe'),('case-09','image','jimeng-images','jimeng_seedream46_cvtob'),('case-01','analysis','openai-text','different-model')]:
            args=copy.copy(self.args);args.case_id=case;args.kind=kind;args.provider=provider;args.model=model
            with patch.object(eval_tool.urllib.request,'build_opener',side_effect=AssertionError('network')),self.assertRaises((ValueError,KeyError)):
                eval_tool.run(args,self.auth,self.report)

    def test_stale_or_changed_config_denies_before_paid_submission(self):
        self.catalog_change=lambda item,count:item.update(version=4) if count==2 else None
        with self.assertRaises(ValueError):self.execute()
        self.assertFalse(self.posts());self.assertFalse(self.args.ledger.exists())

    def test_server_budget_must_not_exceed_cap(self):
        self.budget.update(limit_minor=301,available_minor=301)
        with self.assertRaises(ValueError):self.execute()
        self.assertFalse(self.posts());self.assertFalse(self.args.ledger.exists())

    def test_quote_currency_stale_hash_units_and_amount_rejected(self):
        for changes in ({'currency':'CNY'},{'maximum_minor':11},{'maximum_minor':True},{'minor_units':3},{'request_sha256':'wrong'},{'model_config_version':4},{'expires_at':'2000-01-01T00:00:00Z'},{'source':''}):
            with self.subTest(changes=changes):
                original=copy.deepcopy(self.quote);self.quote.update(changes)
                with self.assertRaises(ValueError):self.execute()
                self.assertFalse(self.posts());self.assertFalse(self.args.ledger.exists())
                self.quote=original

    def test_uncertain_submit_is_never_retried_or_released(self):
        self.submit_error=TimeoutError('synthetic timeout')
        with self.assertRaises(TimeoutError):self.execute()
        self.assertEqual(len(self.posts()),1)
        self.assertEqual(json.loads(self.args.ledger.read_text())['reservations'][0]['state'],'unknown')

    def test_cny_is_not_converted_to_usd(self):
        self.args.currency='CNY';self.args.budget_micros=5000000
        self.quote['currency']='CNY';self.budget.update(currency='CNY',limit_minor=500,available_minor=500)
        self.assertEqual(self.execute(),0)
        self.assertEqual(json.loads(self.posts()[0].data)['budget_currency'],'CNY')
        self.assertEqual(json.loads(self.args.ledger.read_text())['currency'],'CNY')

    def test_fractional_minor_unit_or_missing_voice_denied(self):
        self.args.reserve_micros=100001
        with self.assertRaises(ValueError):eval_tool.validate_execution(self.args)
        self.args.reserve_micros=100000;self.args.kind='speech';self.args.provider='minimax-speech';self.args.model='speech-2.8-turbo';self.args.case_id='case-13'
        with self.assertRaises(ValueError):eval_tool.validate_execution(self.args)

    def test_unknown_exposure_and_symlink_ledger_rejected(self):
        self.args.budget_micros=100000
        eval_tool.reserve(self.args.ledger,self.args,'previous')
        with self.assertRaises(ValueError):eval_tool.reserve(self.args.ledger,self.args,'next')
        self.args.ledger.unlink()
        target=Path(self.folder.name)/'target';target.write_text('do not read or replace')
        self.args.ledger.symlink_to(target)
        with self.assertRaises(OSError):eval_tool.reserve(self.args.ledger,self.args,'symlink')
        self.assertEqual(target.read_text(),'do not read or replace')

    def test_main_uncertain_report_never_contains_credentials_or_exception_body(self):
        report=Path(self.folder.name)/'report.json'
        self.submit_error=TimeoutError('synthetic-secret in raw exception must not be persisted')
        argv=['--kind',self.args.kind,'--provider',self.args.provider,'--model',self.args.model,'--case-id',self.args.case_id,
              '--model-config-id',self.args.model_config_id,'--model-config-version','3','--budget-micros','3000000','--reserve-micros','100000',
              '--report',str(report),'--ledger',str(self.args.ledger),'--execute-live','--authorize-provider-model-budget','--authorize-session-transmission']
        with patch.object(eval_tool.sys,'stdin',io.StringIO(json.dumps(self.auth))),patch.object(eval_tool.urllib.request,'build_opener',return_value=self):
            self.assertEqual(eval_tool.main(argv),1)
        value=json.loads(report.read_text())
        self.assertEqual(value['status'],'incomplete')
        self.assertEqual(value['cost_state'],'unknown_reserved')
        self.assertEqual(value['failure_type'],'TimeoutError')
        self.assertNotIn('synthetic-secret',report.read_text())
        self.assertEqual(len(self.posts()),1)
        self.assertEqual(os.stat(report).st_mode & 0o777,0o600)

    def test_existing_report_prevents_any_network_or_stdin(self):
        report=Path(self.folder.name)/'report';report.write_text('{}')
        argv=['--kind',self.args.kind,'--provider',self.args.provider,'--model',self.args.model,'--case-id',self.args.case_id,
              '--model-config-id',self.args.model_config_id,'--model-config-version','3','--budget-micros','3000000','--reserve-micros','100000',
              '--report',str(report),'--execute-live','--authorize-provider-model-budget','--authorize-session-transmission']
        with patch.object(eval_tool.sys,'stdin') as stdin,patch.object(eval_tool.urllib.request,'build_opener',side_effect=AssertionError('network')):
            stdin.read.side_effect=AssertionError('session read')
            with self.assertRaises(FileExistsError):eval_tool.main(argv)
        self.assertEqual(report.read_text(),'{}')


if __name__ == '__main__':
    unittest.main()

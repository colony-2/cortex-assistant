"""Approved document and runtime expectations; all mutations use disposable fixtures."""
from contextlib import contextmanager
import base64
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
HELPER = ROOT / '.c2/recipes/hooks/build/verify.py'
spec = importlib.util.spec_from_file_location('verify', HELPER)
v = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v)
VALID = '# A responsibility\n\nA useful prose paragraph.\n'


class Documents(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.doc = self.root / 'PURPOSE.md'
        self.doc.write_text(VALID)

    def test_valid_and_reworded(self):
        for text in (VALID, '# Different heading\n\nDifferent\nwrapped prose.\n'):
            self.doc.write_text(text)
            self.assertEqual(v.check_document(self.root)['document_checks'], 1)

    def test_invalid_content(self):
        for data in (b'', b' \n', b'\xff', b'# A\n\nBad\x00text', b'# A\n',
                     b'No title\n', b'# \n\nProse', b'# Title\n\n## Subtitle'):
            with self.subTest(data=data):
                self.doc.write_bytes(data)
                with self.assertRaises((ValueError, UnicodeError)):
                    v.check_document(self.root)

    def test_review_empty_closing_hash_title(self):
        for title in ('# ###', '# #', '# ###   ', '# <!-- placeholder -->'):
            with self.subTest(title=title):
                self.doc.write_text(title + '\n\nUseful prose.\n')
                with self.assertRaisesRegex(ValueError, 'title'):
                    v.check_document(self.root)
        for title in ('# Useful title ###', '# Useful title', '# Useful###'):
            self.doc.write_text(title + '\n\nUseful prose.\n')
            self.assertEqual(v.check_document(self.root)['document_checks'], 1)

    def test_review_comments_are_not_prose(self):
        for body in ('<!-- placeholder -->', '<!-- multi\nline comment\n-->',
                     '<div>placeholder</div>', '    code only', '***', '___'):
            with self.subTest(body=body):
                self.doc.write_text('# Title\n\n' + body + '\n')
                with self.assertRaises(ValueError):
                    v.check_document(self.root)
        self.doc.write_text('# Title ###\n\n<!-- ignored --> Useful prose.\n')
        self.assertEqual(v.check_document(self.root)['document_checks'], 1)
        self.doc.write_text('# Title\n\n<!-- unclosed')
        with self.assertRaisesRegex(ValueError, 'unsupported'):
            v.check_document(self.root)

    def test_review_escaped_links_are_unsupported(self):
        (self.root / 'target').write_text('existing target')
        for form in (r'\[label](target)', r'\![label](target)',
                     r'![label](target) \[label](target)', r'[la\]bel](target)'):
            with self.subTest(form=form):
                self.doc.write_text(VALID + form)
                with self.assertRaisesRegex(ValueError, 'unsupported'):
                    v.check_document(self.root)
        self.doc.write_text(VALID + '[label](target) ![label](target)')
        result = v.check_document(self.root)
        self.assertEqual(result['local_links_checked'], 2)
        self.assertEqual(result['unsupported_forms'], [])

    def test_review_email_autolinks_are_unsupported(self):
        for form in ('<user@example.com>', '<User.Name+tag@example.com>', '<mailto:user@example.com>'):
            with self.subTest(form=form):
                self.doc.write_text(VALID + form)
                with self.assertRaisesRegex(ValueError, 'unsupported'):
                    v.check_document(self.root)
        self.doc.write_text(VALID + '`<user@example.com>` and `\\[label](target)`')
        result = v.check_document(self.root)
        self.assertEqual(result['local_links_checked'], 0)
        self.assertEqual(result['excluded'], [])

    def test_missing_unreadable(self):
        self.doc.unlink()
        with self.assertRaises(FileNotFoundError):
            v.check_document(self.root)
        # Deterministic even when tests run as root.
        with patch.object(Path, 'read_bytes', side_effect=PermissionError('unreadable')):
            with self.assertRaises(PermissionError):
                v.check_document(self.root)

    def test_valid_links_images_and_exclusions(self):
        (self.root / 'target space.txt').write_text('target')
        self.doc.write_text(VALID + '[label](target%20space.txt#part) ![image](target%20space.txt) '
                            '[web](https://example.invalid/a) [net](//example.invalid/a) [part](#part)')
        with patch.object(subprocess, 'run', side_effect=AssertionError('network execution forbidden')):
            result = v.check_document(self.root)
        self.assertEqual(result['local_links_checked'], 2)
        self.assertEqual(len(result['excluded']), 3)
        self.assertEqual(result['unsupported_forms'], [])

    def test_relative_to_document_directory(self):
        (self.root / 'sub').mkdir()
        (self.root / 'sub/target').write_text('ok')
        self.doc.write_text(VALID + '[local](sub/target)')
        self.assertEqual(v.check_document(self.root)['local_links_checked'], 1)

    def test_bad_links(self):
        for link in ('[x](missing)', '[x](../escape)', '[x](%2e%2e/escape)', '[x](/tmp/file)',
                     '[x]()', '[x](a b)', '[x](a(b))', '[x](a "title")', '[x](<a>)',
                     '[x](unclosed', '[x][ref]', '[ref]: target', '[shortcut]',
                     '[nested [x]](target)', '[x](a\\b)', '<https://example.invalid>', '<a href="a">x</a>'):
            with self.subTest(link=link):
                self.doc.write_text(VALID + link)
                with self.assertRaises(ValueError):
                    v.check_document(self.root)

    def test_symlink_escape(self):
        (self.root / 'outside').symlink_to(self.root.parent)
        self.doc.write_text(VALID + '[x](outside)')
        with self.assertRaisesRegex(ValueError, 'escapes'):
            v.check_document(self.root)

    def test_literal_code(self):
        self.doc.write_text(VALID + '\n`[ignored](bad)`\n```md\n[x](bad)\n```\n')
        self.assertEqual(v.check_document(self.root)['local_links_checked'], 0)
        for code in ('`unclosed', '```\nunclosed'):
            self.doc.write_text(VALID + code)
            with self.assertRaises(ValueError):
                v.check_document(self.root)


class Runtime(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'candidate'
        self.root.mkdir()
        (self.root / 'PURPOSE.md').write_text(VALID)
        # These are documentation fixtures, independent of candidate application
        # growth. Real application selection is covered separately below.
        for args in (['init', '-q'], ['add', 'PURPOSE.md'],
                     ['-c', 'user.name=Verifier', '-c', 'user.email=verify@example.invalid',
                      'commit', '-qm', 'disposable document fixture']):
            subprocess.run(['git', '-C', str(self.root), *args], check=True)
        self.head = subprocess.check_output(['git', '-C', str(self.root), 'rev-parse', 'HEAD']).decode().strip()

    def runner(self, **kwargs):
        return v.Verification(self.root, kwargs.pop('candidate', self.head), **kwargs)

    def test_actual_candidate_success_and_failure_integrity(self):
        for text, status in ((VALID, 'passed'), ('# Title only', 'failed')):
            (self.root / 'PURPOSE.md').write_text(text)
            run = self.runner()
            result = run.run()
            self.assertEqual(result['status'], status, result)
            self.assertEqual(result['before'], result['after'])
            self.assertTrue(result['commands'])
            self.assertEqual(result['baseline']['revision'], v.BASELINE)
            if status == 'failed':
                self.assertEqual(result['commands'][-5]['exit_code'], 1)

    def test_preflight_identity_containment_api(self):
        for kwargs in ({'candidate': 'wrong'}, {'directory': '..'}, {'directory': '/tmp'},
                       {'directory': 'missing'}, {'api': 'bad'}):
            result = self.runner(**kwargs).run()
            self.assertEqual(result['status'], 'failed')
            self.assertEqual(result['document']['state'], 'not_attempted')

    def test_missing_git(self):
        with patch.dict(os.environ, {'PATH': ''}):
            result = self.runner().run()
        self.assertEqual(result['status'], 'failed')
        self.assertEqual(result['commands'][0]['outcome'], 'start_error')

    def test_missing_python_operation_error(self):
        hook = (ROOT / '.c2/recipes/hooks/build/verify.yaml').read_text()
        self.assertIn('set -eu', hook)
        result = subprocess.run(['/bin/sh', '-c', 'set -eu; python3 missing.py'],
                                env={'PATH': ''}, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b'python3', result.stderr)

    def test_nonzero_start_timeout(self):
        for args, timeout, outcome in (([sys.executable, '-c', 'import sys; print("recognizable"); '
                                       'print("error",file=sys.stderr); sys.exit(37)'], 2, 'exited'),
                                      (['/nonexistent-verification-executable'], 2, 'start_error'),
                                      ([sys.executable, '-c', 'import time; time.sleep(3)'], .01, 'timeout')):
            run = self.runner()
            with self.assertRaises(RuntimeError):
                run.command(args, timeout)
            self.assertEqual(run.logs[-1]['outcome'], outcome)
            if outcome == 'exited':
                self.assertEqual(run.logs[-1]['exit_code'], 37)
                self.assertIn('recognizable', run.logs[-1]['stdout'])
                self.assertIn('error', run.logs[-1]['stderr'])

    def test_failed_and_timed_out_go_logs_remain_individual_artifacts(self):
        directory = Path(self.temp.name) / 'tools'
        directory.mkdir()
        executable = directory / 'go'
        artifacts = Path(self.temp.name) / 'artifacts'
        artifacts.mkdir()
        for suffix, timeout in (('exit 37', 2), ('/bin/sleep 2', .05)):
            executable.write_text("#!/bin/sh\nprintf 'event\\n'\nprintf 'diagnostic\\n' >&2\n" + suffix + '\n')
            executable.chmod(0o755)
            run = v.Verification(self.root, self.head, artifacts=artifacts)
            run.command_env['PATH'] = str(directory)
            with self.assertRaises(RuntimeError):
                run.command(['go', 'test'], timeout=timeout)
            self.assertIn('event', (artifacts / 'go-test.jsonl').read_text())
            self.assertIn('diagnostic', (artifacts / 'go-test.stderr').read_text())

    def test_mutations_detected_without_repair(self):
        for kind in ('head', 'index', 'tracked'):
            with self.subTest(kind=kind):
                run = self.runner()
                original = run.snapshot
                calls = 0
                def changed():
                    nonlocal calls
                    state = original()
                    calls += 1
                    if calls == 2:
                        if kind == 'head':
                            state['head'] = 'simulated-head-change'
                        elif kind == 'index':
                            state['index_sha256'] = 'simulated-index-change'
                        else:
                            (self.root / 'PURPOSE.md').write_text('mutation remains visible')
                            state = original()
                    return state
                with patch.object(run, 'snapshot', side_effect=changed):
                    result = run.run()
                self.assertEqual(result['status'], 'failed')
                self.assertTrue(any('state changed' in e for e in result['errors']))
                (self.root / 'PURPOSE.md').write_text(VALID)

    def test_unknown_candidate_files_require_mapping(self):
        (self.root / 'app.py').write_text('application')
        subprocess.run(['git', '-C', str(self.root), 'add', 'app.py'], check=True)
        result = self.runner().run()
        self.assertEqual(result['status'], 'failed')
        self.assertIn('mapping requires review', result['errors'][0])
        self.assertEqual(result['document']['state'], 'not_attempted')

    def test_finalization_and_artifacts(self):
        outbox = Path(self.temp.name) / 'outbox'
        outbox.mkdir()
        environment = dict(os.environ, WORKTREE=str(self.root), CANDIDATE=self.head,
                           DIRECTORY='.', API_VERSION='c2.build-hook/v1', OUTBOX=str(outbox))
        before = self.runner().snapshot()
        result = subprocess.run([sys.executable, str(HELPER)], env=environment,
                                cwd='/', capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)['status'], 'passed')
        evidence = json.loads((outbox / 'verification-evidence.json').read_text())
        self.assertEqual(evidence['before'], evidence['after'])
        self.assertEqual(before, self.runner().snapshot())
        environment['OUTBOX'] = str(outbox / 'nonexistent')
        result = subprocess.run([sys.executable, str(HELPER)], env=environment, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(b'"status": "passed"', result.stdout)

    def test_no_checks_cannot_pass(self):
        run = self.runner()
        original = run.command
        def no_checks(args, timeout=10):
            if '--document' in args:
                return b'{"document_checks":0}'
            return original(args, timeout)
        with patch.object(run, 'command', side_effect=no_checks):
            self.assertEqual(run.run()['status'], 'failed')

    def test_document_runtime_errors_finalize_failed(self):
        for failure in (RuntimeError('start error'), RuntimeError('timeout')):
            run = self.runner()
            original = run.command
            def fail(args, timeout=10):
                if '--document' in args:
                    raise failure
                return original(args, timeout)
            with patch.object(run, 'command', side_effect=fail):
                result = run.run()
            self.assertEqual(result['status'], 'failed')
            self.assertIn('after', result)

    def application_fixture(self):
        for name in v.APPLICATION_FILES:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('fixture\n')
        subprocess.run(['git', '-C', str(self.root), 'add', '.'], check=True)
        subprocess.run(['git', '-C', str(self.root), '-c', 'user.name=Verifier', '-c',
                        'user.email=verify@example.invalid', 'commit', '-qm', 'complete fixture'], check=True)
        self.head = subprocess.check_output(['git', '-C', str(self.root), 'rev-parse', 'HEAD']).decode().strip()

    def test_application_mapping_requires_complete_inventory(self):
        (self.root / 'go.mod').write_text('module fixture\n')
        subprocess.run(['git', '-C', str(self.root), 'add', '.'], check=True)
        result = self.runner().run()
        self.assertEqual(result['status'], 'failed')
        self.assertIn('incomplete application coverage', result['errors'][0])

    def test_application_checks_and_failure_propagation(self):
        self.application_fixture()
        for failure in (None, 'gofmt', 'vet', 'test', 'native', 'include', 'hook', 'missing-go', 'untracked'):
            with self.subTest(failure=failure):
                run = self.runner()
                original = run.command
                calls = []
                def command(args, timeout=5):
                    kind = None
                    if args[0] == 'gofmt': kind = 'gofmt'
                    elif args[0] == 'go':
                        kind = {'version': 'missing-go', 'vet': 'vet', 'test': 'test', 'run': 'native', 'list': 'modules'}[args[1]]
                    elif '-m' in args and 'unittest' in args: kind = 'hook'
                    if kind == 'native' and 'validate' in args: kind = 'include'
                    if kind:
                        calls.append(kind)
                        if failure == 'untracked' and kind == 'hook':
                            (self.root / 'unexpected.go').write_text('new source')
                        if failure == kind:
                            if kind == 'gofmt': return b'assistant/client.go\n'
                            raise RuntimeError('injected ' + kind + ' failure')
                        return b'declared-secret-include valid' if kind == 'include' else b''
                    return original(args, timeout)
                with patch.object(run, 'command', side_effect=command), \
                     patch.object(run, 'candidate_environment') as candidate, \
                     patch.object(run, 'validate_cases'), patch.object(run, 'validate_native'):
                    candidate.return_value.__enter__.return_value = {'tree': 'fixture'}
                    result = run.run()
                self.assertEqual(result['status'], 'passed' if failure is None else 'failed', result)
                self.assertEqual(result['before'], result['after'])
                if failure is None:
                    self.assertEqual(calls, ['missing-go', 'gofmt', 'vet', 'test', 'modules', 'native', 'include', 'hook'])
                    self.assertEqual(result['application']['state'], 'passed')
                elif failure == 'untracked':
                    self.assertIn('untracked or modified source', result['errors'][-1])
                    (self.root / 'unexpected.go').unlink()
                else:
                    self.assertNotEqual(result['application']['state'], 'passed')


class Candidates(unittest.TestCase):
    setUp = Runtime.setUp
    runner = Runtime.runner

    def test_exact_commit_full_tree_mirror_and_integrity(self):
        artifacts = Path(self.temp.name) / 'evidence'
        artifacts.mkdir()
        run = self.runner()
        before = run.snapshot()
        with run.candidate_environment(artifacts) as source:
            self.assertEqual(source['commit'], self.head)
            self.assertEqual(source, run.evidence['candidate_source']['mirror'])
            checkout = Path(run.command_env['ASSISTANT_VERIFY_CHECKOUT'])
            self.assertEqual((checkout / 'PURPOSE.md').read_bytes(), (self.root / 'PURPOSE.md').read_bytes())
            self.assertFalse(run.evidence['candidate_source']['synthetic_commit'])
        self.assertEqual(run.snapshot(), before)
        self.assertNotIn('ASSISTANT_VERIFY_CANDIDATE', run.command_env)
        self.assertTrue(all(VALID not in entry.get('stdout', '') for entry in run.logs))
        self.assertTrue(any(entry.get('blob_sha256') for entry in run.logs))

    def test_shallow_mirror_keeps_unpublished_candidate_identity(self):
        subprocess.run(['git', '-C', str(self.root), '-c', 'user.name=Verifier', '-c',
                        'user.email=verify@example.invalid', 'commit', '--allow-empty', '-qm', 'parent'], check=True)
        shallow = Path(self.temp.name) / 'shallow'
        subprocess.run(['git', 'clone', '-q', '--depth=1', self.root.as_uri(), str(shallow)], check=True)
        (shallow / 'unpublished.md').write_text('unpublished candidate fixture')
        subprocess.run(['git', '-C', str(shallow), 'add', '.'], check=True)
        subprocess.run(['git', '-C', str(shallow), '-c', 'user.name=Verifier', '-c',
                        'user.email=verify@example.invalid', 'commit', '-qm', 'unpublished fixture'], check=True)
        candidate = subprocess.check_output(['git', '-C', str(shallow), 'rev-parse', 'HEAD']).decode().strip()
        artifacts = Path(self.temp.name) / 'evidence'
        artifacts.mkdir()
        run = v.Verification(shallow, candidate)
        with run.candidate_environment(artifacts) as source:
            self.assertEqual(source['commit'], candidate)
            self.assertEqual(source, run.evidence['candidate_source']['mirror'])
            self.assertEqual(subprocess.check_output(['git', '-C', run.command_env['ASSISTANT_VERIFY_MIRROR'],
                             'rev-parse', '--is-shallow-repository']).strip(), b'false')
        self.assertEqual(subprocess.check_output(['git', '-C', str(shallow), 'rev-parse',
                         '--is-shallow-repository']).strip(), b'true')

    def test_wrong_bytes_index_and_post_execution_mutation(self):
        artifacts = Path(self.temp.name) / 'evidence'
        artifacts.mkdir()
        (self.root / 'PURPOSE.md').write_text('uncommitted')
        with self.assertRaisesRegex(ValueError, 'bytes mismatch'):
            with self.runner().candidate_environment(artifacts):
                self.fail('dirty source admitted')
        (self.root / 'PURPOSE.md').write_text(VALID)
        # Same content with executable mismatch is also a different candidate.
        (self.root / 'PURPOSE.md').chmod(0o755)
        with self.assertRaisesRegex(ValueError, 'mode mismatch'):
            with self.runner().candidate_environment(artifacts):
                self.fail('mode mismatch admitted')
        (self.root / 'PURPOSE.md').chmod(0o644)
        (self.root / 'new').write_text('new staged source')
        subprocess.run(['git', '-C', str(self.root), 'add', 'new'], check=True)
        with self.assertRaisesRegex(ValueError, 'index or working tree'):
            with self.runner().candidate_environment(artifacts):
                self.fail('staged addition admitted')
        subprocess.run(['git', '-C', str(self.root), 'reset', '-q', 'HEAD', 'new'], check=True)
        (self.root / 'new').unlink()
        run = self.runner()
        with self.assertRaisesRegex(ValueError, 'bytes mismatch'):
            with run.candidate_environment(artifacts):
                checkout = Path(run.command_env['ASSISTANT_VERIFY_CHECKOUT'])
                (checkout / 'PURPOSE.md').write_text('mutation')

    def test_required_native_missing_skips_and_failures(self):
        artifacts = Path(self.temp.name) / 'native'
        artifacts.mkdir()
        run = self.runner()
        expected = ['finish-reaches-success-without-followup-model', 'invalid-then-finish',
                    'confirm-then-finish', 'send-then-finish']
        summary = {'cases': 4, 'results': [dict(case_id=name, status='passed') for name in expected]}
        path = artifacts / 'summary.json'
        with self.assertRaises(FileNotFoundError):
            run.validate_native(artifacts)
        path.write_text(json.dumps(summary))
        run.validate_native(artifacts)
        for status in ('failed', 'skipped'):
            summary['results'][0]['status'] = status
            path.write_text(json.dumps(summary))
            with self.assertRaises(ValueError):
                run.validate_native(artifacts)
        summary['results'].pop()
        path.write_text(json.dumps(summary))
        with self.assertRaises(ValueError):
            run.validate_native(artifacts)

    def case_fixture(self):
        artifacts = Path(self.temp.name) / 'cases-evidence'
        artifacts.mkdir(exist_ok=True)
        tree = 'candidate-tree'
        for test, mode in v.ORDINARY_CASES.items():
            directory = artifacts / 'cases' / test
            directory.mkdir(parents=True, exist_ok=True)
            case = {'test': test, 'mode': mode, 'outcome': 'passed', 'candidate': self.head,
                    'tree': tree, 'canonical_sources': [{'source': {
                        'resolved_commit': self.head,
                        'submitted_selector': 'git+https://github.com/colony-2/cortex-assistant.git//discussion-goal.yaml@' + self.head},
                        'recipe_artifact': 'resolved-recipe.yaml', 'recipe_sha256': v.hashlib.sha256(b'resolved').hexdigest()}],
                    'requests': [{'model': 'fixture'}] if mode in v.POSITIVE_MODES else [],
                    'package': {'sha256_before': v.PACKAGE_HASHES, 'sha256_after': v.PACKAGE_HASHES,
                                'trusted_command_suffix': []}}
            (directory / 'candidate.json').write_text(json.dumps(case))
            for name in v.behavior_artifacts(mode):
                (directory / name).write_text(json.dumps(dict(download_outcome='unavailable', replay_outcome='unavailable', reopen_outcome='unavailable')))
            if mode == 'goal-retention':
                case['inspection_phase'] = 'source/task inspection before intentional loss'
                (directory / 'candidate.json').write_text(json.dumps(case))
            (directory / 'resolved-recipe.yaml').write_bytes(b'resolved')
            (directory / 'initial-prompt.json').write_text(json.dumps({'assistant': {'text': 'provider grounded reply'}}))
        contract = artifacts / 'cases' / 'TestPinnedInferenceHTTPSConcurrencyAndDeadline'
        contract.mkdir(parents=True, exist_ok=True)
        (contract / 'inference-contract.json').write_text(json.dumps({
            'candidate': self.head, 'overlapping_clients': 2, 'deadline_outcome': 'timeout',
            'deadline_requests': 3, 'invocation_timeout_seconds': 2, 'deadline_from_first_request_ms': 1900,
            'requests': [{}] * 5, 'results': [{'model': 'first'}, {'model': 'second'}],
            'package': {'selector': v.INFERENCE, 'sha256_before': v.PACKAGE_HASHES, 'sha256_after': v.PACKAGE_HASHES}}))
        events = [dict(Package='github.com/colony-2/cortex-assistant/assistant', Test=test, Action='pass')
                  for test in set(v.ORDINARY_CASES) | v.COMPATIBILITY_CASES]
        events += [dict(Package='github.com/colony-2/cortex-assistant/cmd/' + name, Action='skip')
                   for name in ('assistant-op', 'discussion-op')]
        return artifacts, {'tree': tree}, events

    def test_required_cases_identity_requests_package_artifacts_and_skips(self):
        run = self.runner()
        artifacts, source, events = self.case_fixture()
        def validate():
            return run.validate_cases(('\n'.join(json.dumps(event) for event in events)).encode(), artifacts, source)
        validate()
        directory = artifacts / 'cases' / 'TestGoalSuccessfulProviderReopen'
        path = directory / 'candidate.json'
        original = json.loads(path.read_text())
        for key, value in (('candidate', 'wrong'), ('tree', 'wrong'), ('outcome', 'failed'),
                           ('requests', []), ('package', {}), ('canonical_sources', [])):
            changed = dict(original, **{key: value})
            path.write_text(json.dumps(changed))
            with self.subTest(key=key), self.assertRaises(ValueError):
                validate()
        changed = json.loads(json.dumps(original))
        changed['package']['sha256_after']['bin/pydantic'] = 'modified'
        path.write_text(json.dumps(changed))
        with self.assertRaisesRegex(ValueError, 'inference mutated'):
            validate()
        path.write_text(json.dumps(original))
        (directory / 'initial-prompt.json').write_text(json.dumps({'assistant': {'text': '', 'error': 'caught provider error'}}))
        with self.assertRaisesRegex(ValueError, 'validated response'):
            validate()
        (directory / 'initial-prompt.json').write_text(json.dumps({'assistant': {'text': 'provider grounded reply'}}))
        path.unlink()
        with self.assertRaises(FileNotFoundError):
            validate()
        path.write_text(json.dumps(original))
        events[0]['Action'] = 'skip'
        with self.assertRaisesRegex(ValueError, 'failed/skipped'):
            validate()
        events[0]['Action'] = 'fail'
        with self.assertRaisesRegex(ValueError, 'failed/skipped'):
            validate()
        events.pop(0)
        with self.assertRaisesRegex(ValueError, 'missing required Go pass'):
            validate()



class WrapperIntegration(unittest.TestCase):
    def test_actual_shared_wrapper_and_resolved_artifacts(self):
        # Local recipe testing executes the real wrapper, never a mock wrapper.
        revision = '84a889884fcf78f44de9ab80dcfb99ddf5329d5e'
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / 'recipes'
            subprocess.run(['git', 'clone', '-q', '--no-checkout',
                            'https://github.com/colony-2/recipes.git', str(source)], check=True)
            subprocess.run(['git', '-C', str(source), 'checkout', '-q', '--detach', revision], check=True)
            template = (ROOT / 'tests/verify-wrapper.yaml').read_text()
            # Recipe artifact bindings are a literal map, not a template for
            # the entire map. Bind every stress file to its catalog reference.
            bindings = []
            for i in range(148):
                name = 'evidence-%03d.json' % i
                query = ('{"kind":"stored", "name":"' + name + '", "stored":{"key":'
                    '(.producer + {"name":"' + name + '", "sizeBytes":.sizes["' + name + '"]})}}')
                expression = ("${{ inputs.stress ? jq(json_parse(sequence.read_catalog.outputs.stdout), " + repr(query) +
                    ") : sequence.actual_wrapper.outputs.artifact_refs['hooks/verify/0/verification-evidence.json'] }}")
                bindings.append('    ' + name + ': ' + json.dumps(expression))
            bindings = '\n'.join(bindings)
            template = template.replace('    # Stress fixture bindings are inserted here by test_verify.py.', bindings)
            (source / 'verify-wrapper.yaml').write_text(template)
            files = {name: (ROOT / name).read_text() for name in (
                '.c2/recipes/hooks/build/verify.yaml', '.c2/recipes/hooks/build/verify.py',
                '.c2/recipes/hooks/build/verification.md')}
            cases = []
            scenarios = (
                ('valid', (ROOT / 'PURPOSE.md').read_text(), 'passed', True, ''),
                ('invalid', '# Title only\n', 'failed', False, 'prose'),
                ('empty-title', '# ###\n\nUseful prose.\n', 'failed', False, 'title'),
                ('comment-only', '# Title\n\n<!-- placeholder -->\n', 'failed', False, 'prose'),
                ('escaped-link', VALID + r'\[label](PURPOSE.md)', 'failed', False, 'unsupported'),
                ('email-autolink', VALID + '<user@example.com>', 'failed', False, 'unsupported'),
                ('closing-title', '# Useful title ###\n\nUseful prose.\n', 'passed', True, ''),
                ('supported-links', VALID + '[label](PURPOSE.md) ![image](PURPOSE.md)', 'passed', True, ''),
                ('many-artifacts', VALID, 'passed', True, ''),
                ('many-artifacts-failed', '# Title only\n', 'failed', False, 'prose'),
            )
            for name, document, status, allowed, diagnostic in scenarios:
                fixture_files = dict(files, **{'PURPOSE.md': document})
                if name.startswith('many-artifacts'):
                    # Exercise the real hook schema with >64 stored files.
                    # Reserved names are supplied at the export expression:
                    # applications cannot write runtime bookkeeping to outbox.
                    fixture_files['.c2/recipes/hooks/build/verify.py'] = files[
                        '.c2/recipes/hooks/build/verify.py'].replace(
                            "    run = Verification(os.environ['WORKTREE']",
                            "    for i in range(148):\n"
                            "        (outbox / ('evidence-%03d.json' % i)).write_text(json.dumps({'case': i}))\n"
                            "    run = Verification(os.environ['WORKTREE']")
                    fixture_files['.c2/recipes/hooks/build/verify.yaml'] = files[
                        '.c2/recipes/hooks/build/verify.yaml'].replace(
                            'jq(sequence.verify.artifacts,',
                            'jq(jq(sequence.verify.artifacts, \' . + {'
                            '"__git_state_thin_pack__": .["verification-evidence.json"], '
                            '"diff_from_parent.diff": .["verification-evidence.json"], '
                            '"diff_from_base.diff": .["verification-evidence.json"]}\'),')
                cases.append({'id': name, 'type': 'integration_case',
                    'runtime': {'cells': {'root': {'files': fixture_files}}},
                    'inputs': {'stress': name.startswith('many-artifacts')}, 'mocks': {'ops': [{'repeat': True,
                    'match': {'op': 'command_execution'}, 'behavior': {'mode': 'passthrough'}}]},
                    'assertions': [{'type': 'output_equals', 'path': 'result.status', 'value': status},
                        {'type': 'output_equals', 'path': 'allowed', 'value': allowed},
                        {'type': 'cel_true', 'expr': 'size(outputs.artifact_refs) >= 2'}]})
            suite = source / 'verification-cases.yaml'
            suite.write_text(json.dumps({'recipe': 'verify-wrapper.yaml', 'cases': cases}))
            output = Path(temporary) / 'results'
            command = ['c2j', 'test', 'run', '--file', str(suite), '--cell', str(ROOT),
                       '--artifact-mode', 'inline', '--artifact-max-bytes', '200000',
                       '--out-dir', str(output)]
            process = subprocess.run(command, capture_output=True, text=True, timeout=300)
            # Keep the original wrapper evidence even if a later assertion fails.
            if os.environ.get('VERIFY_TEST_EVIDENCE'):
                retained = Path(os.environ['VERIFY_TEST_EVIDENCE'])
                retained.mkdir(parents=True, exist_ok=True)
                (retained / 'command.json').write_text(json.dumps({
                    'args': command, 'exit_code': process.returncode,
                    'stdout': process.stdout, 'stderr': process.stderr}, indent=2) + '\n')
                if output.exists():
                    shutil.copytree(output, retained, dirs_exist_ok=True)
            self.assertEqual(process.returncode, 0, process.stdout + process.stderr)
            for name, document, expected, allowed, diagnostic in scenarios:
                result = json.loads((output / 'cases' / name / 'result.json').read_text())
                self.assertEqual(result['status'], 'passed', result)
                run = result['run']
                self.assertEqual(run['outputs']['result']['status'], expected)
                self.assertEqual(run['outputs']['allowed'], expected == 'passed')
                calls = run['runtime']['calls']
                artifacts = {key: value for call in calls for key, value in call.get('artifacts', {}).items()}
                refs = run['outputs']['artifact_refs']
                self.assertLessEqual(len(refs), 64)
                reserved = {'__git_state_thin_pack__', 'diff_from_parent.diff', 'diff_from_base.diff'}
                self.assertFalse(reserved & {Path(key).name for key in refs})
                # The real shared wrapper prefixes and materializes value refs.
                catalog_key = 'hooks/verify/0/artifact-catalog.json'
                self.assertEqual(refs[catalog_key]['kind'], 'attached')
                catalog_refs = json.loads(artifacts[catalog_key])
                self.assertFalse(reserved & catalog_refs.keys())
                self.assertEqual(refs['hooks/verify/0/verification-evidence.json']['stored'],
                                 catalog_refs['verification-evidence.json']['stored'])
                if name.startswith('many-artifacts'):
                    downloaded = json.loads(base64.b64decode(
                        run['artifacts']['downloaded-evidence.json']['content_base64']))
                    self.assertEqual(len(refs), 3)  # two hook refs plus wrapper report
                    self.assertEqual(sum(key.startswith('evidence-') for key in catalog_refs), 148)
                    self.assertEqual(len(downloaded), 149)
                    for key, item in downloaded.items():
                        self.assertEqual(item['size'], catalog_refs[key]['stored']['key']['sizeBytes'])
                    for i in range(148):
                        key = 'evidence-%03d.json' % i
                        self.assertEqual(catalog_refs[key]['kind'], 'stored')
                        self.assertEqual(catalog_refs[key]['stored']['key']['name'], key)
                        self.assertEqual(downloaded[key]['value'], {'case': i})
                evidence = json.loads(artifacts['hooks/verify/0/verification-evidence.json'])
                self.assertEqual(evidence['status'], expected)
                self.assertEqual(evidence['before'], evidence['after'])
                self.assertTrue(evidence['commands'])
                if expected == 'failed':
                    self.assertTrue(any(c.get('exit_code') == 1 and diagnostic in c.get('stderr', '')
                                        for c in evidence['commands']))
                else:
                    self.assertGreater(evidence['document']['document_checks'], 0)
                if diagnostic == 'unsupported':
                    self.assertTrue(evidence['document']['unsupported_forms'])
                    self.assertEqual(evidence['document'].get('local_links_checked', 0), 0)
                if name == 'supported-links':
                    self.assertEqual(evidence['document']['local_links_checked'], 2)
                    self.assertEqual(evidence['document']['unsupported_forms'], [])
                self.assertTrue(run['runtime']['repositories']['root']['clean'])


if __name__ == '__main__':
    unittest.main()

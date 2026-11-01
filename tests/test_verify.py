"""Approved document and runtime expectations; all mutations use disposable fixtures."""
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
        subprocess.run(['git', 'clone', '-q', '--shared', str(ROOT), str(self.root)], check=True)
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
            )
            for name, document, status, allowed, diagnostic in scenarios:
                cases.append({'id': name, 'type': 'integration_case',
                    'runtime': {'cells': {'root': {'files': dict(files, **{'PURPOSE.md': document})}}},
                    'inputs': {}, 'mocks': {'ops': [{'repeat': True,
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
            process = subprocess.run(command, capture_output=True, text=True, timeout=90)
            self.assertEqual(process.returncode, 0, process.stdout + process.stderr)
            for name, document, expected, allowed, diagnostic in scenarios:
                result = json.loads((output / 'cases' / name / 'result.json').read_text())
                self.assertEqual(result['status'], 'passed', result)
                run = result['run']
                self.assertEqual(run['outputs']['result']['status'], expected)
                self.assertEqual(run['outputs']['allowed'], expected == 'passed')
                calls = run['runtime']['calls']
                artifacts = {key: value for call in calls for key, value in call.get('artifacts', {}).items()}
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
            # Preserve exact local runtime evidence when requested by the caller.
            if os.environ.get('VERIFY_TEST_EVIDENCE'):
                shutil.copytree(output, os.environ['VERIFY_TEST_EVIDENCE'], dirs_exist_ok=True)


if __name__ == '__main__':
    unittest.main()

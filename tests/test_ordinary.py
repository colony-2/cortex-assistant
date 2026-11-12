"""Real ordinary helper/inspection regression; no policy operation is intercepted."""
import json
import hashlib
import copy
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
C2J = 'github.com/colony-2/c2j/cmd/c2j@v0.0.125-0.20261111181458-c2193c57d890'


class OrdinaryHelper(unittest.TestCase):
    def test_capture_then_assemble_reads_actual_record(self):
        with tempfile.TemporaryDirectory(prefix='ordinary-helper-') as temporary:
            directory = Path(temporary)
            sources = {str(p.relative_to(ROOT)): os.path.relpath(p, directory)
                       for p in (ROOT / 'assistant').glob('*.go')
                       if not p.name.endswith('_test.go')}
            for name in ('go.mod', 'go.sum', 'cmd/discussion-op/main.go',
                         'ops/discussion/op.yaml'):
                sources[name] = os.path.relpath(ROOT / name, directory)
            suite = {'recipe': os.path.relpath(ROOT / 'recipe-tests/ordinary-capture.yaml', directory),
                     'cases': [{'id': 'actual-helper-capture-assemble', 'type': 'integration_case',
                       'runtime': {'cells': {'root': {'file_sources': sources}}},
                       'inputs': {}, 'mocks': {'ops': [{'repeat': True,
                       'match': {'op': 'extension_execution'}, 'behavior': {'mode': 'passthrough'}}]},
                       'assertions': [
                         {'type': 'output_equals', 'path': 'assembled.protocol', 'value': 'cortex-assistant/3'},
                         {'type': 'cel_true', 'expr': 'outputs.assembled.model_input.prompt.contains("A global discussion")'}]}]}
            text = 'Selected evidence 世界\n' * 150 + 'tail-marker-after-1000'
            documents = []
            for name, producer, task, content in (
                    ('first', 'original-child', 17, text),
                    ('second', 'other-child', 8, 'Independent second document')):
                documents.append({'id': name, 'producer': {'job_id': producer, 'task': task},
                    'mime': 'text/plain', 'outcome': 'included', 'text': content,
                    'sha256': hashlib.sha256(content.encode()).hexdigest(), 'size': len(content.encode())})
            multi = copy.deepcopy(suite['cases'][0])
            multi['id'] = 'multi-document-original-producers'
            multi['inputs'] = {'snapshot': {'source': {'kind': 'story', 'job_id': 'parent'},
                                           'documents': documents}}
            multi['inputs']['model_response'] = json.dumps({'text': 'grounded in both sources',
                'documents': [], 'return_artifact_ids': [], 'citations': ['first', 'second']})
            multi['assertions'] += [
                {'type': 'output_equals', 'path': 'response.citations', 'value': ['first', 'second']},
                {'type': 'cel_true', 'expr': 'outputs.assembled.model_input.prompt.contains("tail-marker-after-1000")'},
                {'type': 'cel_true', 'expr': 'outputs.assembled.model_input.prompt.contains("original-child")'},
                {'type': 'cel_true', 'expr': 'outputs.assembled.model_input.prompt.contains("other-child")'},
                {'type': 'output_equals', 'path': 'assembled.coverage.first', 'value': 'included'},
                {'type': 'output_equals', 'path': 'assembled.coverage.second', 'value': 'included'}]
            suite['cases'].append(multi)
            suite['cases'][0]['assertions'].append(
                {'type': 'output_equals', 'path': 'response.text', 'value': 'grounded'})
            for name, response in (
                    ('unknown-citation', {'text': 'bad', 'documents': [], 'return_artifact_ids': [], 'citations': ['forged']}),
                    ('executable-output', {'text': 'bad', 'documents': [], 'return_artifact_ids': [], 'citations': [], 'tools': ['shell']}),
                    ('malformed-output', None)):
                negative = copy.deepcopy(suite['cases'][0])
                negative['id'] = name
                negative['inputs'] = {'model_response': json.dumps(response) if response is not None else '{broken'}
                negative['assertions'] = [
                    {'type': 'output_equals', 'path': 'response.error',
                     'value': 'The model response could not be validated.'}]
                suite['cases'].append(negative)
            path = directory / 'case.json'
            path.write_text(json.dumps(suite))
            output = directory / 'results'
            command = ['go', 'run', C2J, 'test', 'run', '--file', str(path),
                       '--cell', str(ROOT), '--out-dir', str(output)]
            result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, timeout=240)
            if os.environ.get('ASSISTANT_ORDINARY_EVIDENCE') and output.exists():
                target = Path(os.environ['ASSISTANT_ORDINARY_EVIDENCE'])
                shutil.copytree(output, target, dirs_exist_ok=True)
                (target / 'command.json').write_text(json.dumps({'argv': command, 'exit': result.returncode,
                     'stdout': result.stdout, 'stderr': result.stderr}, indent=2))
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            case = json.loads((output / 'cases/actual-helper-capture-assemble/result.json').read_text())
            self.assertEqual(case['status'], 'passed', case)
            calls = case['run']['runtime']['calls']
            self.assertEqual(len([c for c in calls if c['op'] == 'extension_execution']), 3)


if __name__ == '__main__':
    unittest.main()

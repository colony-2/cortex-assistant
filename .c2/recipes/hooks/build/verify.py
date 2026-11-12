"""Read-only repository verification; see verification.md."""
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from urllib.parse import unquote, urlsplit

BASELINE = 'd2c4a33352f3f0e888b172946fca69caffd573df'
# Explicitly reviewed inventory: additions still require a coverage decision.
APPLICATION_FILES = frozenset({
    'README.md',
    'assistant/activity.go',
    'assistant/activity_test.go',
    'assistant/assistant_test.go',
    'assistant/client.go',
    'assistant/candidate_verification_test.go',
    'assistant/consumer_integration_test.go',
    'assistant/discovery.go',
    'assistant/dispatch_discovery.go',
    'assistant/dispatch_discovery_test.go',
    'assistant/dispatch_recovery_integration_test.go',
    'assistant/frozen_start.go',
    'assistant/frozen_start_test.go',
    'assistant/goal_lifecycle.go',
    'assistant/goal_lifecycle_test.go',
    'assistant/goal_records.go',
    'assistant/goal_test.go',
    'assistant/goal_unstarted.go',
    'assistant/goal_unstarted_test.go',
    'assistant/goal_worker_integration_test.go',
    'assistant/guard_integration_test.go',
    'assistant/lifecycle_race_integration_test.go',
    'assistant/lineage.go',
    'assistant/ordinary.go',
    'assistant/ordinary_attachments.go',
    'assistant/ordinary_attachments_test.go',
    'assistant/ordinary_boundaries_test.go',
    'assistant/ordinary_client.go',
    'assistant/ordinary_client_test.go',
    'assistant/ordinary_dispatch.go',
    'assistant/ordinary_dispatch_test.go',
    'assistant/ordinary_example_test.go',
    'assistant/ordinary_guard_integration_test.go',
    'assistant/ordinary_test.go',
    'assistant/ordinary_worker_integration_test.go',
    'assistant/provider_integration_test.go',
    'assistant/registration.go',
    'assistant/releases.go',
    'assistant/releases_test.go',
    'assistant/replay.go',
    'assistant/respond_errors_test.go',
    'assistant/retention_integration_test.go',
    'assistant/runtime_restoration_test.go',
    'assistant/snapshot.go',
    'assistant/snapshot_test.go',
    'assistant/source_capture_integration_test.go',
    'assistant/tasks.go',
    'assistant/types.go',
    'assistant/uploads.go',
    'assistant/validate.go',
    'assistant/worker_integration_test.go',
    'cmd/assistant-op/main.go',
    'cmd/discussion-op/main.go',
    'discussion-goal.yaml',
    'discussion.yaml',
    'docs/design.md',
    'docs/hosting.md',
    'docs/pending-input-agreement.md',
    'docs/test-statements.md',
    'go.mod',
    'go.sum',
    'inference.yaml',
    'ops/assistant/op.yaml',
    'ops/discussion/op.yaml',
    'recipe-tests/inference-host.yaml',
    'recipe-tests/inference.test.yaml',
    'recipe-tests/ordinary-capture.yaml',
    'recipe-tests/session.test.yaml',
    'session.yaml',
    'tests/test_ordinary.py',
})
C2J_CLI = 'github.com/colony-2/c2j/cmd/c2j@v0.0.125-0.20261111181458-c2193c57d890'
OMISSIONS = ('Actual-provider execution (opt-in, separately bound evidence); '
             'public assistant remote fetchability; Cortex consumer integration; '
             'hosted CI runners/caches/platform matrices; publishing/deployment.')

# Every ordinary mode is mandatory; one full suite supplies these case artifacts.
ORDINARY_CASES = {
    'TestOrdinaryWorkerInputAndFinish': 'binding',
    'TestOrdinaryWorkerStructuredProvider': 'success',
    'TestOrdinaryWorkerProviderUnknownCitation': 'citation',
    'TestOrdinaryFacadeRuntimeBoundary': 'facade',
    'TestGoalSequentialReopenAfterReplacement': 'goal',
    'TestGoalSuccessfulProviderReopen': 'goal-provider',
    'TestGoalQueuedCloseAndReopen': 'goal-queued',
    'TestGoalOrdinaryConfirmedDispatch': 'goal-dispatch',
    'TestGoalOrdinaryTerminalDispatchDiscovery': 'goal-dispatch-loss',
    'TestGoalSendCloseRaceAndUncertainClose': 'goal-send-close',
    'TestGoalCompletionCloseRace': 'goal-completion-race',
    'TestGoalActualRetentionLoss': 'goal-retention',
    'TestGoalLargeHistoryAcrossReopen': 'goal-large',
    'TestGoalProcessingCloseAndReopen': 'goal-processing',
    'TestGoalExplicitlyFailedCannotReopen': 'goal-closed-failed',
    'TestGoalSucceededCannotReopen': 'goal-closed-succeeded',
    'TestGoalExactReviewSourceRuntime': 'goal-review',
}
GUARDS = ('empty-endpoint empty-model empty-provider invalid-provider prefixed-model '
          'missing-secret empty-secret ambient redirect citation executable malformed '
          'provider-error retry injection untrusted-tls').split()
ORDINARY_CASES.update({'TestOrdinaryResolvedGuardMatrix/' + name: 'goal-guard-' + name
                       for name in GUARDS})
COMPATIBILITY_CASES = {
    'TestFrozenV2Descriptor', 'TestLegacyDescriptorIdentity',
    'TestGoalHostExplicitLegacyMappings', 'TestHistoricalLimitsThroughCurrentHost',
    'TestHistoricalLimitBoundaries', 'TestHistoricalSameProtocolLimits',
    'TestWorkerLegacyPendingThroughCurrentHost', 'TestPendingInputRestoration',
    'TestFrozenStartSurvivesReplacementAndUncertainReceipt',
    'TestGoalCloseCapturedQueuedProcessingAndWaiting',
    'TestPinnedInferenceHTTPSConcurrencyAndDeadline',
}
POSITIVE_MODES = {'success', 'goal-provider', 'goal-dispatch', 'goal-dispatch-loss',
                  'goal-retention', 'goal-large', 'goal-processing', 'goal-review',
                  'goal-guard-retry', 'goal-guard-injection'}
BEHAVIOR_ARTIFACTS = {
    'binding': ['facade-runtime.json'], 'success': ['structured-provider.json'],
    'citation': ['unknown-citation-provider.json'], 'facade': ['facade-runtime.json'],
    'goal': ['sequential-reopen.json'],
    'goal-provider': ['sequential-reopen.json', 'provider-requests.json'],
    'goal-review': ['actual-review-source.json', 'actual-review-model.json'],
    'goal-retention': ['actual-retention-loss.json'], 'goal-send-close': ['send-close-race.json'],
}


def behavior_artifacts(mode):
    if mode.startswith('goal-guard-'):
        return ['guard-' + mode.removeprefix('goal-guard-') + '.json']
    return BEHAVIOR_ARTIFACTS.get(mode, [mode + '.json'])


PACKAGE_HASHES = {
    'bin/pydantic': '989549dce9440bd3068a38bd24e7ac8c99aa27cedeb97a2bc012f37c86ab42da',
    'libexec/main.py': '6d8bee84fc9f17fb1daf42d89cfd842df31b0a1ea0704dd09efff4819134b989',
    'share/c2j/op.json': 'c5b4d3ce5c44208e3df7bc8e5f4721bf3eb096604b76d0a6507608070275e082',
}
INFERENCE = 'nix:github:colony-2/c2ops/6396e1fe6462084e85ea44d86d372bdf3b133db0#pydantic'



def literal_free(text):
    """Exclude fenced and inline code, failing ambiguous/unclosed delimiters."""
    lines, fence = [], None
    for line in text.splitlines():
        match = re.match(r'^\s{0,3}(`{3,}|~{3,})(.*)$', line)
        if match:
            marker = match[1]
            if fence is None:
                fence = marker
            elif marker[0] == fence[0] and len(marker) >= len(fence) and not match[2].strip():
                fence = None
            continue
        if fence is None:
            lines.append(line)
    if fence:
        raise ValueError('unsupported unclosed code fence')
    text = '\n'.join(lines)
    out, position = [], 0
    for match in re.finditer(r'(`+)(.*?)\1', text, re.S):
        out.append(text[position:match.start()])
        out.append(' ')
        position = match.end()
    out.append(text[position:])
    text = ''.join(out)
    if '`' in text:
        raise ValueError('unsupported unclosed inline code')
    return text


def check_document(root):
    path = root / 'PURPOSE.md'
    if not path.resolve().is_relative_to(root):
        raise ValueError('PURPOSE.md escapes repository')
    data = path.read_bytes()
    text = data.decode('utf-8', errors='strict')
    if not text.strip() or b'\x00' in data:
        raise ValueError('empty/whitespace document or NUL byte')
    text = literal_free(text)
    # Comments have no rendered title/prose content. Leave a separator so words
    # on opposite sides cannot join into an accidental destination or title.
    text = re.sub(r'<!--.*?-->', ' ', text, flags=re.S)
    if '<!--' in text:
        raise ValueError('unsupported unclosed HTML comment')
    titles = re.findall(r'^ {0,3}#[ \t]+(.*)$', text, re.M)
    # ATX closing hashes are decoration, including an all-hash empty title.
    titles = [re.sub(r'(?:^|[ \t]+)#+[ \t]*$', '', title).strip() for title in titles]
    if not any(titles):
        raise ValueError('missing nonempty top-level title')
    # Fail unsupported forms before consuming any supported link substrings.
    if re.search(r'\\[!\[\]]', text):
        raise ValueError('unsupported escaped Markdown link form')
    if re.search(r'<(?:[A-Za-z][\w+.-]*:|//|[^<>\s]+@[^<>\s]+>)', text):
        raise ValueError('unsupported autolink')
    if re.search(r'</?[A-Za-z][A-Za-z0-9-]*(?:\s[^<>]*|/?)>|<[!?][^>]*>', text):
        raise ValueError('unsupported HTML block or link')
    prose = [line for line in text.splitlines() if line.strip()
             and not re.match(r'^(?: {4}|\t)', line)
             and not re.match(r'^\s*(?:#|>|[-*+]\s|\d+[.)]\s|---+$|\|)', line)
             and not re.fullmatch(r'[ \t]{0,3}(?:(?:\*[ \t]*){3,}|(?:_[ \t]*){3,}|(?:-[ \t]*){3,})', line)
             and not re.fullmatch(r'\s*!?\[.*\]\(.*\)\s*', line)]
    if not prose:
        raise ValueError('missing prose paragraph outside title')
    pattern = re.compile(r'!?\[([^\[\]\\\n]+)\]\(([^\s()<>\'"\\]+)\)')
    checked, excluded = [], []
    remainder, position = [], 0
    for match in pattern.finditer(text):
        remainder.append(text[position:match.start()])
        position = match.end()
        destination = match[2]
        parts = urlsplit(destination)
        if parts.scheme or parts.netloc:
            excluded.append({'destination': destination, 'reason': 'external; not fetched'})
            continue
        if destination.startswith('#'):
            excluded.append({'destination': destination, 'reason': 'fragment-only; not checked'})
            continue
        decoded = unquote(parts.path, errors='strict')
        if not decoded or Path(decoded).is_absolute():
            raise ValueError('invalid repository-relative destination: ' + destination)
        target = (path.parent / decoded).resolve()
        if not target.is_relative_to(root):
            raise ValueError('destination escapes repository: ' + destination)
        if not target.exists():
            raise ValueError('missing local target: ' + destination)
        checked.append({'destination': destination, 'target': str(target.relative_to(root)),
                        'fragment': 'not checked' if parts.fragment else None})
    remainder.append(text[position:])
    if '[' in ''.join(remainder) or ']' in ''.join(remainder):
        raise ValueError('unsupported or malformed Markdown link form')
    return {'document_checks': 1, 'local_links_checked': len(checked),
            'checked': checked, 'excluded': excluded, 'unsupported_forms': [],
            'stdout': 'UTF-8, content, title, prose and supported destinations passed\n'}


class Verification:
    def __init__(self, root, candidate, directory='.', api='c2.build-hook/v1', artifacts=None):
        self.root = Path(root).resolve()
        self.candidate, self.directory, self.api = candidate, directory, api
        self.logs = []
        self.command_env = dict(os.environ, PYTHONDONTWRITEBYTECODE="1")
        self.artifacts = Path(artifacts).resolve() if artifacts else None
        self.evidence = {'baseline': {'revision': BASELINE, 'tracked_inventory': ['PURPOSE.md'],
                          'workflows': [], 'mapping': 'No CI or implementation at inspected main.'},
                         'omissions': OMISSIONS, 'commands': self.logs,
                         'document': {'state': 'not_attempted', 'document_checks': 0},
                         'application': {'state': 'not_present'},
                         'errors': [], 'tools': {'python': sys.version.split()[0]}}

    def retain_go_output(self, args, stdout, stderr):
        if self.artifacts and args[:2] == ['go', 'test']:
            (self.artifacts / 'go-test.jsonl').write_bytes(stdout)
            (self.artifacts / 'go-test.stderr').write_bytes(stderr)

    def command(self, args, timeout=5):
        entry = {'argv': args, 'cwd': str(self.root)}
        self.logs.append(entry)
        try:
            result = subprocess.run(args, cwd=self.root, capture_output=True, timeout=timeout, env=self.command_env)
            entry.update(stdout=result.stdout.decode('utf-8', 'backslashreplace'),
                         stderr=result.stderr.decode('utf-8', 'backslashreplace'),
                         exit_code=result.returncode, outcome='exited')
            self.retain_go_output(args, result.stdout, result.stderr)
            if result.returncode:
                raise RuntimeError('command failed with exit ' + str(result.returncode))
            return result.stdout
        except subprocess.TimeoutExpired as error:
            entry.update(outcome='timeout', exit_code=None,
                         stdout=(error.stdout or b'').decode('utf-8', 'backslashreplace'),
                         stderr=(error.stderr or b'').decode('utf-8', 'backslashreplace'))
            self.retain_go_output(args, error.stdout or b'', error.stderr or b'')
            raise RuntimeError('command timed out') from error
        except OSError as error:
            entry.update(outcome='start_error', exit_code=None, stdout='', stderr=str(error))
            raise RuntimeError('command could not start: ' + str(error)) from error

    def git(self, *args):
        return self.command(['git', *args])

    def snapshot(self):
        head = self.git('rev-parse', 'HEAD').decode().strip()
        index_path = Path(os.fsdecode(self.git('rev-parse', '--git-path', 'index')).strip())
        if not index_path.is_absolute():
            index_path = self.root / index_path
        inventory = self.git('ls-files', '-z').split(b'\0')
        files = {}
        for raw in filter(None, inventory):
            name = os.fsdecode(raw)
            path = self.root / name
            if path.is_symlink():
                files[name] = {'symlink': os.readlink(path)}
            elif path.is_file():
                files[name] = {'sha256': hashlib.sha256(path.read_bytes()).hexdigest(),
                               'mode': path.stat().st_mode}
            elif not path.exists():
                files[name] = {'missing': True}
            else:
                raise ValueError('unsupported tracked entry: ' + name)
        return {'head': head, 'index_sha256': hashlib.sha256(index_path.read_bytes()).hexdigest(),
                'staged_entries': self.git('ls-files', '--stage').decode('utf-8', 'backslashreplace'),
                'tracked_files': files}

    def manifest(self, repository):
        def git(*args):
            return self.command(['git', '-C', str(repository), *args], timeout=120)
        entries = git('ls-tree', '-rz', self.candidate).split(b'\0')
        files = {}
        for entry in filter(None, entries):
            metadata, raw_name = entry.split(b'\t', 1)
            mode, kind, oid = metadata.decode().split()
            if kind != 'blob':
                raise ValueError('candidate contains unsupported non-blob entry')
            name = os.fsdecode(raw_name)
            data = git('cat-file', 'blob', oid)
            # Source bytes can contain fixture credentials. Retain object/hash
            # provenance without reproducing every source file in diagnostics.
            self.logs[-1]['stdout'] = '<Git blob bytes hashed; content omitted>\n'
            self.logs[-1]['blob_sha256'] = hashlib.sha256(data).hexdigest()
            self.logs[-1]['blob_bytes'] = len(data)
            files[name] = dict(mode=mode, oid=oid, sha256=hashlib.sha256(data).hexdigest())
        return {'commit': git('rev-parse', self.candidate + '^{commit}').decode().strip(),
                'tree': git('rev-parse', self.candidate + '^{tree}').decode().strip(),
                'files': files}

    def assert_candidate_bytes(self, manifest, checkout):
        for name, entry in manifest['files'].items():
            path = checkout / name
            if entry['mode'] == '120000':
                data = os.fsencode(os.readlink(path))
            else:
                if path.is_symlink():
                    raise ValueError('candidate symlink mismatch: ' + name)
                data = path.read_bytes()
                executable = bool(path.stat().st_mode & 0o111)
                if executable != (entry['mode'] == '100755'):
                    raise ValueError('candidate executable mode mismatch: ' + name)
            if hashlib.sha256(data).hexdigest() != entry['sha256']:
                raise ValueError('candidate bytes mismatch: ' + name)

    @contextmanager
    def candidate_environment(self, artifacts):
        # All mutations occur outside the managed checkout. Never create a commit.
        source = self.manifest(self.root)
        if source['commit'] != self.candidate:
            raise ValueError('candidate identity mismatch')
        self.assert_candidate_bytes(source, self.root)
        if self.git('--no-optional-locks', 'status', '--porcelain', '--untracked-files=all').strip():
            raise ValueError('candidate index or working tree differs from commit (including untracked files)')
        with tempfile.TemporaryDirectory(prefix='assistant-candidate-') as temporary:
            scratch = Path(temporary)
            mirror, checkout = scratch / 'candidate.git', scratch / 'checkout'
            self.command(['git', 'clone', '--mirror', '--no-hardlinks', str(self.root), str(mirror)], timeout=120)
            if self.command(['git', '-C', str(mirror), 'rev-parse', '--is-shallow-repository']).strip() == b'true':
                origin = self.git('remote', 'get-url', 'origin').decode().strip()
                self.command(['git', '-C', str(mirror), 'fetch', '--unshallow', origin], timeout=120)
            self.command(['git', 'clone', '--no-hardlinks', str(mirror), str(checkout)], timeout=120)
            self.command(['git', '-C', str(checkout), 'checkout', '--detach', self.candidate], timeout=120)
            received = self.manifest(mirror)
            if received != source or self.manifest(checkout) != source:
                raise ValueError('candidate mirror full-tree identity mismatch')
            self.assert_candidate_bytes(source, checkout)
            self.evidence['candidate_source'] = {'managed': source, 'mirror': received,
                'transport': 'supported tenant Git profile: canonical HTTPS assistant URL to read-only local HTTP mirror',
                'public_fetchability_proved': False, 'synthetic_commit': False}
            (artifacts / 'candidate-manifest.json').write_text(json.dumps(source, indent=2) + '\n')
            previous = self.command_env
            self.command_env = dict(previous, ASSISTANT_VERIFY_CANDIDATE=self.candidate,
                ASSISTANT_VERIFY_MIRROR=str(mirror), ASSISTANT_VERIFY_CHECKOUT=str(checkout),
                ASSISTANT_VERIFY_EVIDENCE=str(artifacts / 'cases'))
            try:
                yield source
            finally:
                self.command_env = previous
                self.assert_candidate_bytes(source, checkout)
                self.assert_candidate_bytes(source, self.root)
                if self.manifest(mirror) != source:
                    raise ValueError('candidate mirror mutated')
                if self.command(['git', '-C', str(checkout), 'status', '--porcelain']).strip():
                    raise ValueError('candidate scratch checkout mutated')

    def validate_cases(self, output, artifacts, source):
        outcomes = {}
        for line in output.decode().splitlines():
            event = json.loads(line)
            test, action = event.get('Test'), event.get('Action')
            if test and action in ('pass', 'skip', 'fail'):
                outcomes[(event['Package'], test)] = action
            skip_allowed = ((test == 'TestWorkerActualProvider' and event['Package'] ==
                             'github.com/colony-2/cortex-assistant/assistant') or
                            (test is None and event['Package'] in {
                                'github.com/colony-2/cortex-assistant/cmd/assistant-op',
                                'github.com/colony-2/cortex-assistant/cmd/discussion-op'}))
            if action == 'fail' or (action == 'skip' and not skip_allowed):
                raise ValueError('required Go execution failed/skipped: ' + str(test))
        package = 'github.com/colony-2/cortex-assistant/assistant'
        for test in set(ORDINARY_CASES) | COMPATIBILITY_CASES:
            if outcomes.get((package, test)) != 'pass':
                raise ValueError('missing required Go pass: ' + test)
        cases = []
        for test, mode in ORDINARY_CASES.items():
            directory = artifacts / 'cases' / test
            case = json.loads((directory / 'candidate.json').read_text())
            if (case['test'], case['mode'], case['outcome'], case['candidate'], case['tree']) != (
                    test, mode, 'passed', self.candidate, source['tree']):
                raise ValueError('candidate case identity/outcome mismatch: ' + test)
            sources = case['canonical_sources']
            if not sources:
                raise ValueError('missing canonical resolution evidence: ' + test)
            for item in sources:
                recipe_bytes = (directory / item['recipe_artifact']).read_bytes()
                if hashlib.sha256(recipe_bytes).hexdigest() != item['recipe_sha256']:
                    raise ValueError('resolved recipe artifact hash mismatch: ' + test)
                resolved = item['source']
                if (resolved['resolved_commit'] != self.candidate or
                        not resolved['submitted_selector'].endswith('@' + self.candidate)):
                    raise ValueError('wrong resolved candidate: ' + test)
            if mode in POSITIVE_MODES - {'goal-processing'}:
                prompt = json.loads((directory / 'initial-prompt.json').read_text())
                if prompt['assistant'].get('error') or prompt['assistant'].get('text') != 'provider grounded reply':
                    raise ValueError('positive case lacks validated response: ' + test)
            if mode in POSITIVE_MODES and not case['requests']:
                raise ValueError('positive case has no actual provider request: ' + test)
            if mode == 'goal-guard-untrusted-tls' and case['requests']:
                raise ValueError('untrusted provider reached application: ' + test)
            if (mode in POSITIVE_MODES or mode.startswith('goal-guard-') or mode == 'citation') and not case['package']:
                raise ValueError('missing inference identity: ' + test)
            if case['package']:
                for phase in ('sha256_before', 'sha256_after'):
                    if case['package'][phase] != PACKAGE_HASHES:
                        raise ValueError('immutable inference mutated: ' + test)
                if case['package']['trusted_command_suffix']:
                    raise ValueError('modified inference command: ' + test)
            # Each case must retain its behavior proof, in addition to identity metadata.
            for name in behavior_artifacts(mode):
                json.loads((directory / name).read_text())
            if mode == 'goal-retention':
                loss = json.loads((directory / 'actual-retention-loss.json').read_text())
                if (not case.get('inspection_phase', '').startswith('source/task inspection before') or
                        any(loss[key] != 'unavailable' for key in
                            ('download_outcome', 'replay_outcome', 'reopen_outcome'))):
                    raise ValueError('retention evidence lacks before/after loss boundary')
            cases.append({'test': test, 'mode': mode, 'status': 'passed',
                          'artifacts': [str(path.relative_to(artifacts)) for path in sorted(directory.iterdir()) if path.is_file()]})
        contract_path = artifacts / 'cases' / 'TestPinnedInferenceHTTPSConcurrencyAndDeadline' / 'inference-contract.json'
        contract = json.loads(contract_path.read_text())
        if (contract['candidate'] != self.candidate or contract['overlapping_clients'] != 2 or
                contract['deadline_outcome'] != 'timeout' or contract['deadline_requests'] < 3 or
                contract['invocation_timeout_seconds'] != 2 or contract['deadline_from_first_request_ms'] > 3500 or
                len(contract['requests']) < 5 or {result['model'] for result in contract['results']} != {'first', 'second'} or
                contract['package']['selector'] != INFERENCE or
                any(contract['package'][phase] != PACKAGE_HASHES for phase in ('sha256_before', 'sha256_after'))):
            raise ValueError('missing/failed immutable inference isolation/deadline contract')
        result = {'cases': cases, 'dependency_contract': str(contract_path.relative_to(artifacts)), 'test_outcomes': [dict(package=p, test=t, outcome=o)
                  for (p, t), o in sorted(outcomes.items())],
                  'inference': INFERENCE, 'trust': 'child-only NIX_SSL_CERT_FILE; HTTPS verify=True',
                  'required_cases': len(cases), 'status': 'passed'}
        (artifacts / 'case-index.json').write_text(json.dumps(result, indent=2) + '\n')
        index = ['# Candidate ordinary verification', '',
                 'Candidate: `' + self.candidate + '`. Tree: `' + source['tree'] + '`.', '',
                 '[Full candidate manifest](candidate-manifest.json); [Go event log](go-test.jsonl); '
                 '[Required case outcomes](case-index.json); [inference isolation/deadline](' + str(contract_path.relative_to(artifacts)) + ').', '',
                 'Source transport: canonical HTTPS assistant selectors to exact-candidate local HTTP Git mirror. '
                 'Provider: unchanged pinned inference, HTTPS fixture and child-only NIX_SSL_CERT_FILE. '
                 'Public fetchability and live-provider behavior are outside this evidence.', '']
        for case in cases:
            index.append('- ' + case['test'] + ': ' + ', '.join(
                '[' + Path(name).name + '](' + name + ')' for name in case['artifacts']))
        (artifacts / 'evidence-index.md').write_text('\n'.join(index) + '\n')
        self.evidence['ordinary_acceptance'] = result

    def validate_native(self, artifacts):
        summary = json.loads((Path(artifacts) / 'summary.json').read_text())
        expected = {'finish-reaches-success-without-followup-model', 'invalid-then-finish',
                    'confirm-then-finish', 'send-then-finish'}
        results = summary['results']
        if (summary['cases'] != 4 or len(results) != 4 or
                {case['case_id'] for case in results} != expected or
                any(case['status'] != 'passed' for case in results)):
            raise ValueError('missing/failed required native cases')
        self.evidence['native_cases'] = [{'case': case['case_id'], 'status': case['status']}
                                         for case in results]

    def run(self):
        before = None
        try:
            if self.api != 'c2.build-hook/v1':
                raise ValueError('unsupported api_version')
            top = Path(os.fsdecode(self.git('rev-parse', '--show-toplevel')).strip()).resolve()
            if top != self.root:
                raise ValueError('managed root is not repository root')
            before = self.snapshot()
            self.evidence['before'] = before
            if before['head'] != self.candidate:
                raise ValueError('runtime candidate does not equal HEAD')
            target = (self.root / self.directory).resolve()
            if not target.is_relative_to(self.root) or not target.is_dir():
                raise ValueError('target_directory escapes repository or does not exist')
            self.evidence['tools']['git'] = self.git('--version').decode().strip()
            names = before['tracked_files']
            allowed = {'PURPOSE.md', '.c2/config.yaml', '.c2/recipes/hooks/build/verify.yaml',
                       '.c2/recipes/hooks/build/verify.py', '.c2/recipes/hooks/build/verification.md',
                       'tests/test_verify.py', 'tests/verify.test.yaml', 'tests/verify-wrapper.yaml'}
            unknown = sorted(set(names) - allowed - APPLICATION_FILES)
            if unknown:
                raise ValueError('coverage mapping requires review for candidate files: ' + ', '.join(unknown))
            application = bool(set(names) & APPLICATION_FILES)
            if application:
                missing = sorted(APPLICATION_FILES - set(names))
                if missing:
                    raise ValueError('incomplete application coverage inventory: ' + ', '.join(missing))
                self.evidence['application'] = {'state': 'pending',
                    'files': sorted(APPLICATION_FILES),
                    'checks': ['gofmt', 'go vet', 'go test', 'native recipe cases', 'hook tests']}
            # Run document checking in a bounded child for independent exit/log evidence.
            self.evidence['document']['state'] = 'attempted'
            result = self.command([sys.executable, str(Path(__file__).resolve()), '--document', str(self.root)])
            self.evidence['document'] = dict(json.loads(result), state='passed')
            if self.evidence['document']['document_checks'] < 1:
                raise ValueError('no document checks executed')
            if application:
                self.evidence['application']['state'] = 'attempted'
                self.command(['go', 'version'])
                gofiles = sorted(name for name in names if name.endswith('.go'))
                unformatted = self.command(['gofmt', '-l', *gofiles])
                if unformatted.strip():
                    raise ValueError('unformatted Go files: ' + unformatted.decode().strip())
                self.command(['go', 'vet', './...'], timeout=300)
                with tempfile.TemporaryDirectory(prefix='assistant-evidence-') as temporary:
                    artifacts = self.artifacts or Path(temporary)
                    with self.candidate_environment(artifacts) as source:
                        output = self.command(['go', 'test', './...', '-json', '-count=1', '-timeout=14m'], timeout=900)
                        (artifacts / 'go-test.jsonl').write_bytes(output)
                        self.validate_cases(output, artifacts, source)
                self.evidence['effective_modules'] = self.command(['go', 'list', '-m', '-json', 'all'], timeout=120).decode()
                with tempfile.TemporaryDirectory(prefix='assistant-native-') as native_temporary:
                    artifacts = str(self.artifacts / 'native') if self.artifacts else native_temporary
                    self.command(['go', 'run', C2J_CLI, 'test', 'run', '--file',
                        'recipe-tests/session.test.yaml', '--out-dir', artifacts], timeout=600)
                    self.validate_native(artifacts)
                include = self.command(['go', 'run', C2J_CLI, 'test', 'validate', '--file',
                    'recipe-tests/inference.test.yaml'], timeout=600)
                if b'declared-secret-include valid' not in include:
                    raise ValueError('missing declared-secret include validation')
                self.command([sys.executable, '-m', 'unittest', 'discover', '-s', 'tests', '-v'], timeout=300)
                self.evidence['application']['state'] = 'passed'

        except Exception as error:
            self.evidence['errors'].append(str(error))
            if self.evidence['document']['state'] == 'attempted':
                diagnostic = self.logs[-1].get('stderr', str(error)) if self.logs else str(error)
                self.evidence['document'].update(state='failed', diagnostic=diagnostic,
                    unsupported_forms=[diagnostic] if 'unsupported' in diagnostic else [])
        finally:
            if before is not None:
                try:
                    after = self.snapshot()
                    self.evidence['after'] = after
                    if after != before:
                        raise ValueError('candidate HEAD/index/tracked state changed')
                    if (self.evidence['application']['state'] in ('attempted', 'passed') and
                            self.git('--no-optional-locks', 'status', '--porcelain', '--untracked-files=all').strip()):
                        raise ValueError('candidate acquired untracked or modified source')
                except Exception as error:
                    self.evidence['errors'].append('integrity: ' + str(error))
        failed = bool(self.evidence['errors']) or self.evidence['document']['state'] != 'passed'
        self.evidence['status'] = 'failed' if failed else 'passed'
        self.evidence['summary'] = ('Repository verification failed; see evidence.' if failed else
            ('Repository verification passed: document, Go, native recipe and hook checks.'
             if self.evidence['application']['state'] == 'passed' else
             'Documentation-only fixture verification passed.'))
        return self.evidence


def main():
    if len(sys.argv) == 3 and sys.argv[1] == '--document':
        try:
            print(json.dumps(check_document(Path(sys.argv[2]).resolve())))
        except Exception as error:
            print(str(error), file=sys.stderr)
            return 1
        return 0
    outbox = Path(os.environ['OUTBOX']).resolve()
    run = Verification(os.environ['WORKTREE'], os.environ['CANDIDATE'],
                       os.environ['DIRECTORY'], os.environ['API_VERSION'], artifacts=outbox)
    if outbox.is_relative_to(run.root):
        raise ValueError('outbox must be outside candidate repository')
    evidence = run.run()
    (outbox / 'verification-evidence.json').write_text(json.dumps(evidence, indent=2) + '\n')
    print(json.dumps({key: evidence[key] for key in ('status', 'summary')}))
    return 0


if __name__ == '__main__':
    sys.exit(main())

"""Read-only documentation verification. Standard library only; see verification.md."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import unquote, urlsplit

BASELINE = 'd2c4a33352f3f0e888b172946fca69caffd573df'
OMISSIONS = ('Application compilation/lint/tests; serving protocol; durability/restart; '
             'tenancy; model adaptation; rendering; goals; consumer integration; '
             'hosted CI runners/caches/platform matrices; publishing/deployment.')


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
    def __init__(self, root, candidate, directory='.', api='c2.build-hook/v1'):
        self.root = Path(root).resolve()
        self.candidate, self.directory, self.api = candidate, directory, api
        self.logs = []
        self.evidence = {'baseline': {'revision': BASELINE, 'tracked_inventory': ['PURPOSE.md'],
                          'workflows': [], 'mapping': 'No CI or implementation at inspected main.'},
                         'omissions': OMISSIONS, 'commands': self.logs,
                         'document': {'state': 'not_attempted', 'document_checks': 0},
                         'errors': [], 'tools': {'python': sys.version.split()[0]}}

    def command(self, args, timeout=5):
        entry = {'argv': args, 'cwd': str(self.root)}
        self.logs.append(entry)
        try:
            result = subprocess.run(args, cwd=self.root, capture_output=True, timeout=timeout)
            entry.update(stdout=result.stdout.decode('utf-8', 'backslashreplace'),
                         stderr=result.stderr.decode('utf-8', 'backslashreplace'),
                         exit_code=result.returncode, outcome='exited')
            if result.returncode:
                raise RuntimeError('command failed with exit ' + str(result.returncode))
            return result.stdout
        except subprocess.TimeoutExpired as error:
            entry.update(outcome='timeout', exit_code=None,
                         stdout=(error.stdout or b'').decode('utf-8', 'backslashreplace'),
                         stderr=(error.stderr or b'').decode('utf-8', 'backslashreplace'))
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
            allowed = {'PURPOSE.md', '.c2/recipes/hooks/build/verify.yaml',
                       '.c2/recipes/hooks/build/verify.py', '.c2/recipes/hooks/build/verification.md',
                       'tests/test_verify.py', 'tests/verify.test.yaml', 'tests/verify-wrapper.yaml'}
            unknown = sorted(set(names) - allowed)
            if unknown:
                raise ValueError('coverage mapping requires review for candidate files: ' + ', '.join(unknown))
            # Run document checking in a bounded child for independent exit/log evidence.
            self.evidence['document']['state'] = 'attempted'
            result = self.command([sys.executable, str(Path(__file__).resolve()), '--document', str(self.root)])
            self.evidence['document'] = dict(json.loads(result), state='passed')
            if self.evidence['document']['document_checks'] < 1:
                raise ValueError('no document checks executed')
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
                except Exception as error:
                    self.evidence['errors'].append('integrity: ' + str(error))
        failed = bool(self.evidence['errors']) or self.evidence['document']['state'] != 'passed'
        self.evidence['status'] = 'failed' if failed else 'passed'
        self.evidence['summary'] = ('Documentation-only verification failed; see evidence.' if failed else
            'Documentation-only verification passed; assistant implementation/CI absent at baseline ' + BASELINE + '.')
        return self.evidence


def main():
    if len(sys.argv) == 3 and sys.argv[1] == '--document':
        try:
            print(json.dumps(check_document(Path(sys.argv[2]).resolve())))
        except Exception as error:
            print(str(error), file=sys.stderr)
            return 1
        return 0
    run = Verification(os.environ['WORKTREE'], os.environ['CANDIDATE'],
                       os.environ['DIRECTORY'], os.environ['API_VERSION'])
    evidence = run.run()
    # Artifact failure must propagate; never emit a success before evidence is durable.
    outbox = Path(os.environ['OUTBOX']).resolve()
    if outbox.is_relative_to(run.root):
        raise ValueError('outbox must be outside candidate repository')
    (outbox / 'verification-evidence.json').write_text(json.dumps(evidence, indent=2) + '\n')
    print(json.dumps({key: evidence[key] for key in ('status', 'summary')}))
    return 0


if __name__ == '__main__':
    sys.exit(main())

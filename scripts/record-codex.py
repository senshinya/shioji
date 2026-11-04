#!/usr/bin/env python3
"""Record disposable real app-server conversations, never account/auth messages.

Live uses the user's existing Codex login through Codex itself. Loopback records
real app-server/tool/request behavior against a deterministic local model stub.
No raw diagnostic stderr, authentication payloads or original user projects.
"""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import queue
import shutil
import subprocess
import tempfile
import threading
import time

METHODS = {
    'turn/started', 'turn/completed', 'item/started', 'item/completed',
    'item/agentMessage/delta', 'item/commandExecution/outputDelta',
    'item/reasoning/summaryTextDelta', 'item/tool/requestUserInput',
    'item/commandExecution/requestApproval', 'item/fileChange/requestApproval',
    'serverRequest/resolved', 'thread/tokenUsage/updated', 'turn/plan/updated',
}

class Provider(http.server.BaseHTTPRequestHandler):
    counts = {}
    def log_message(self, *args):
        pass
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        text = json.dumps(body.get('input', []))
        kind = 'question' if 'SHIOJI_QUESTION' in text else 'tool' if 'SHIOJI_TOOL' in text else 'text'
        count = self.counts.get(kind, 0)
        self.counts[kind] = count + 1
        events = [{'type': 'response.created', 'response': {'id': 'response_' + kind + str(count)}}]
        if count == 0 and kind == 'question':
            args = {'questions': [{'id': 'color', 'header': 'Choice', 'question': 'Which neutral surface?', 'options': [{'label': 'Gray', 'description': 'Neutral gray'}, {'label': 'White', 'description': 'Neutral white'}]}]}
            item = {'type': 'function_call', 'call_id': 'question_call', 'name': 'request_user_input', 'arguments': json.dumps(args)}
        elif count == 0 and kind == 'tool':
            item = {'type': 'function_call', 'call_id': 'tool_call', 'name': 'exec_command', 'arguments': json.dumps({'cmd': 'printf SHIOJI_TOOL_OK', 'login': False})}
        else:
            item = {'type': 'message', 'id': 'message_' + kind + str(count), 'role': 'assistant', 'content': [{'type': 'output_text', 'text': 'Recorded ' + kind + ' complete.'}]}
        events += [{'type': 'response.output_item.done', 'item': item}, {'type': 'response.completed', 'response': {'id': 'response_' + kind + str(count), 'usage': {'input_tokens': 11, 'output_tokens': 5, 'total_tokens': 16, 'input_tokens_details': {'cached_tokens': 3}}}}]
        data = ''.join('event: ' + e['type'] + '\ndata: ' + json.dumps(e) + '\n\n' for e in events).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

class Recorder:
    def __init__(self, binary, cwd, env):
        self.p = subprocess.Popen([binary, 'app-server', '--listen', 'stdio://'], cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, bufsize=1)
        self.q = queue.Queue()
        self.events = []
        self.counter = 0
        threading.Thread(target=self.read, daemon=True).start()
    def read(self):
        try:
            for line in self.p.stdout:
                self.q.put(json.loads(line))
        finally:
            self.q.put(None)
    def send(self, message):
        self.p.stdin.write(json.dumps(message) + '\n')
        self.p.stdin.flush()
    def next(self):
        msg = self.q.get(timeout=120)
        if msg is None:
            raise RuntimeError('app-server exited')
        if msg.get('method') in METHODS:
            self.events.append({'direction': 'in', 'message': msg})
        return msg
    def call(self, method, params):
        self.counter += 1
        n = self.counter
        self.send({'id': n, 'method': method, 'params': params})
        while True:
            msg = self.next()
            if msg.get('id') == n and 'method' not in msg:
                if 'error' in msg:
                    raise RuntimeError(method + ': ' + str(msg['error'].get('code')))
                return msg['result']
    def finish(self, turn):
        while True:
            msg = self.next()
            method = msg.get('method')
            if method == 'item/tool/requestUserInput':
                response = {'id': msg['id'], 'result': {'answers': {'color': {'answers': ['Gray']}}}}
                self.events.append({'direction': 'out', 'message': response})
                self.send(response)
            if method == 'turn/completed' and msg['params']['turn']['id'] == turn:
                if msg['params']['turn']['status'] != 'completed':
                    raise RuntimeError('turn did not complete')
                return
    def close(self):
        self.p.stdin.close()
        try:
            self.p.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.p.terminate()
            self.p.wait(timeout=5)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--kind', choices=['live', 'loopback'], required=True)
    ap.add_argument('--out', required=True)
    ap.add_argument('--codex', default=shutil.which('codex'))
    args = ap.parse_args()
    version = subprocess.check_output([args.codex, '--version'], text=True).strip()
    if version != 'codex-cli 0.166.0':
        raise RuntimeError('recording requires pinned codex-cli 0.166.0')
    r = None
    server = None
    with tempfile.TemporaryDirectory(prefix='shioji-record-') as base:
        cwd = Path(base) / 'workspace'
        cwd.mkdir()
        env = os.environ.copy()
        if args.kind == 'loopback':
            home = Path(base) / 'home'
            home.mkdir()
            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            (home / 'config.toml').write_text('model="fixture-model"\nmodel_provider="fixture"\n[model_providers.fixture]\nname="Fixture"\nbase_url="http://127.0.0.1:%d/v1"\nwire_api="responses"\nrequires_openai_auth=false\nsupports_websockets=false\nrequest_max_retries=0\nstream_max_retries=0\n' % server.server_port)
            env['CODEX_HOME'] = str(home)
        try:
            r = Recorder(args.codex, str(cwd), env)
            r.call('initialize', {'clientInfo': {'name': 'shioji_recording', 'version': '0.1.0'}, 'capabilities': {'experimentalApi': True}})
            r.send({'method': 'initialized', 'params': {}})
            catalog = r.call('model/list', {'limit': 100, 'includeHidden': False})
            model = next((m for m in catalog['data'] if m['isDefault']), catalog['data'][0])['model']
            scenarios = ['text', 'tool', 'question'] if args.kind == 'loopback' else ['text']
            for scenario in scenarios:
                thread = r.call('thread/start', {'cwd': str(cwd), 'model': model, 'approvalPolicy': 'never', 'sandbox': 'read-only'})['thread']['id']
                prompts = ['Reply exactly SHIOJI_RECORDING_READY. Do not use tools.', 'Reply exactly SHIOJI_RECORDING_DONE. Do not use tools.'] if args.kind == 'live' else ['SHIOJI_' + scenario.upper()]
                for prompt in prompts:
                    params = {'threadId': thread, 'input': [{'type': 'text', 'text': prompt}]}
                    if scenario == 'question':
                        params['collaborationMode'] = {'mode': 'plan', 'settings': {'model': model, 'reasoning_effort': 'medium', 'developer_instructions': None}}
                    turn = r.call('turn/start', params)['turn']['id']
                    # turn/completed may precede the RPC response; don't lose it.
                    if not any(e['message'].get('method') == 'turn/completed' and e['message']['params']['turn']['id'] == turn for e in r.events):
                        r.finish(turn)
            encoded = '\n'.join(json.dumps(e, ensure_ascii=False, separators=(',', ':')) for e in r.events) + '\n'
            encoded = encoded.replace(base, '/fixture').replace(str(Path.home()), '/fixture/user')
            out = Path(args.out)
            out.parent.mkdir(parents=True, exist_ok=True)
            out.write_text(encoded)
            meta = {'source': 'real-app-server-' + args.kind, 'codex': version, 'codexSHA256': hashlib.sha256(Path(args.codex).resolve().read_bytes()).hexdigest(), 'recordedAt': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), 'events': len(r.events), 'sha256': hashlib.sha256(encoded.encode()).hexdigest(), 'sanitization': 'Allowlisted conversation notifications only; disposable workspace paths replaced; auth/config/catalog/RPC responses and stderr excluded.', 'modelService': 'real-user-account' if args.kind == 'live' else 'deterministic-loopback-provider'}
            out.with_suffix('.meta.json').write_text(json.dumps(meta, indent=2) + '\n')
            print(json.dumps({'source': meta['source'], 'events': len(r.events), 'out': str(out)}))
        finally:
            if r:
                r.close()
            if server:
                server.shutdown()
                server.server_close()

if __name__ == '__main__':
    main()

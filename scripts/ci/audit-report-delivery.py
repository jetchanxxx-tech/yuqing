"""Hosted-only report API audit against prebuilt Go and the actual Python engine.

This intentionally returns failure when an acceptance check fails. It neither
changes product behavior nor contacts live collectors, models, or recipients.
"""
from contextlib import ExitStack
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import io
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tempfile
import threading
import time
import traceback
import xml.etree.ElementTree as ET
import zipfile

import httpx
import psycopg
from psycopg.types.json import Jsonb

ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / 'test-results/report-quality'
API = 'http://127.0.0.1:8086'
ENGINE = 'http://127.0.0.1:8007'
PROXY = 'http://127.0.0.1:8008'
DSN = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_report_quality_test?sslmode=disable'
CHECKS = []


def check(name, passed, **observed):
    entry = {'name': name, 'passed': bool(passed), 'observed': observed}
    CHECKS.append(entry)
    print(json.dumps(entry, ensure_ascii=False), flush=True)


def uid():
    alphabet = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'
    value = (int(time.time() * 1000) << 80) | secrets.randbits(80)
    return ''.join(alphabet[(value >> (5 * n)) & 31] for n in range(25, -1, -1))


def docx_text(data):
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        assert archive.testzip() is None
        xml = ET.fromstring(archive.read('word/document.xml'))
        return '\n'.join(node.text or '' for node in xml.iter()
                         if node.tag.endswith('}t'))


def main():
    assert os.environ.get('GITHUB_ACTIONS') == 'true'
    assert os.environ.get('RUNNER_ENVIRONMENT') == 'github-hosted'
    assert os.environ['YUQING_TEST_PG_URL'] == DSN
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    processes = []
    wire = []
    engine_responses = []
    transport = httpx.Client(timeout=30, trust_env=False)

    def sql(query, parameters=()):
        with psycopg.connect(DSN) as connection:
            with connection.cursor() as cursor:
                cursor.execute(query, parameters)
                return cursor.fetchall() if cursor.description else []

    def api(method, path, token='', data=None):
        return transport.request(method, API + '/api/v1' + path, json=data,
            headers={'Authorization': 'Bearer ' + token} if token else {})

    class RecordingProxy(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
            value = json.loads(body)
            wire.append({key: val for key, val in value.items() if key != 'api_key'})
            response = transport.post(ENGINE + self.path, content=body,
                headers={'Content-Type': 'application/json',
                         'X-Internal-Token': self.headers.get('X-Internal-Token', '')})
            engine_responses.append({'status': response.status_code,
                'body': response.text if response.status_code != 200 else '<binary>'})
            self.send_response(response.status_code)
            self.send_header('Content-Type', response.headers.get('Content-Type', 'application/octet-stream'))
            self.send_header('Content-Length', str(len(response.content)))
            self.end_headers()
            self.wfile.write(response.content)

    proxy = ThreadingHTTPServer(('127.0.0.1', 8008), RecordingProxy)
    threading.Thread(target=proxy.serve_forever, daemon=True).start()
    try:
        with ExitStack() as stack, tempfile.TemporaryDirectory(prefix='yuqing-report-audit-') as folder:
            temporary = Path(folder)
            config = temporary / 'backend.json'
            config.write_text(json.dumps({
                'server': {'addr': '127.0.0.1:8086', 'env': 'test'},
                'store': {'driver': 'postgres'}, 'queue': {'driver': 'postgres'},
                'db': {'primary': DSN, 'maxConns': 5},
                'auth': {'jwtSecret': 'report-audit-local-fixture-only', 'accessTTL': '15m', 'refreshTTL': '1h'},
                # Only the report transport is exercised; no worker is launched.
                'engines': {'query': {'url': 'http://127.0.0.1:1'},
                            'report': {'url': PROXY}}, 'rsshub_base': '',
            }))
            secret = secrets.token_hex(24)
            environment = {key: value for key, value in os.environ.items()
                           if not any(term in key.upper() for term in ('API_KEY', 'PASSWORD', 'TOKEN', 'SECRET'))}
            environment.update(YUQING_CONFIG=str(config), YUQING_BILLING_SERVICE_TOKEN=secret,
                               PYTHONPATH=str(ROOT), LLM_API_KEY='', DEEPSEEK_API_KEY='',
                               YUQING_BILLING_URL=API, YUQING_USAGE_OUTBOX_DIR=str(temporary / 'outbox'))
            subprocess.run([str(ROOT / 'platform/bin/yuqing-cli'), 'migrate', 'platform'],
                           env=environment, check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)

            def start(arguments, name, health):
                log = stack.enter_context((EVIDENCE / name).open('w'))
                process = subprocess.Popen(arguments, cwd=ROOT, env=environment, stdout=log, stderr=subprocess.STDOUT)
                processes.append(process)
                for _ in range(150):
                    if process.poll() is not None:
                        raise RuntimeError(name + ' exited before health')
                    try:
                        if transport.get(health).status_code == 200:
                            return
                    except httpx.HTTPError:
                        pass
                    time.sleep(0.1)
                raise RuntimeError(name + ' health deadline')

            start([sys.executable, '-m', 'uvicorn', 'engines.report_engine.main:app',
                   '--host', '127.0.0.1', '--port', '8007'], 'python-engine.log', ENGINE + '/health')
            start([str(ROOT / 'platform/bin/yuqing-server')], 'go-server.log', API + '/api/v1/health')

            def register(prefix):
                response = api('POST', '/auth/register', data={
                    'email': prefix + '-' + secrets.token_hex(5) + '@example.invalid',
                    'password': secrets.token_urlsafe(24), 'name': '报告沙箱验收'})
                assert response.status_code == 201, response.status_code
                return response.json()

            owner, other = register('report-owner'), register('report-other')
            token = owner['access_token']
            tenant_id, user_id = owner['user']['tenant_id'], owner['user']['user_id']
            response = api('GET', '/reports', token)
            check('empty report list is a non-null array', response.status_code == 200 and response.json()['reports'] == [])
            response = api('GET', '/reports/not-existing', token)
            check('unknown report is 404', response.status_code == 404, status=response.status_code)
            analysis_id, html_id, word_id, md_id, pdf_id, v2_id = uid(), uid(), uid(), uid(), uid(), uid()
            docs = [{'id': 'audit-direct-01', 'title': '系列续航实测', 'content': '新品续航反馈：长途录像仍有电。',
                     'source_type': 'toutiao', 'source_name': '今日头条',
                     'url': 'https://www.toutiao.com/article/123/', 'final_url': 'https://www.toutiao.com/article/123/',
                     'published_at': '2026-10-15T10:00:00+08:00', 'content_kind': 'body',
                     'semantic_classification': 'direct', 'date_sources': {'body': '2026-10-15T10:00:00+08:00'}}]
            dimensions = [{'id': 'heat', 'name': '传播热度', 'findings': 'QA_DIMENSION_FINDING，不能推算全网声量。',
                           'data_points': [], 'quotes': [], 'trend': 'stable', 'confidence': 0.8}]
            v1 = '<!doctype html><html><body><h1>QA_REPORT_V1</h1><p>first snapshot</p></body></html>'
            v2 = '<!doctype html><html><body><h1>QA_REPORT_V2</h1><p>second snapshot</p></body></html>'
            sql('''INSERT INTO analyses(id,tenant_id,name,state,progress,keywords,sources,created_by,
                doc_count,summary,report_content,dimensions,sentiments,topics,scenario_template_id,
                report_template_id,date_from,date_to,research_question)
                VALUES(%s,%s,%s,'completed',100,%s,%s,%s,1,%s,%s,%s,%s,%s,'product_launch','event',
                       '2026-10-01','2026-10-20',%s)''',
                (analysis_id, tenant_id, 'QA完整分析名称', Jsonb(['iphone18','iphone18pro']),
                 Jsonb(['toutiao']), user_id, 'QA_SAVED_SUMMARY', v1, Jsonb(dimensions),
                 Jsonb([{'document_id': docs[0]['id'], 'sentiment': 'positive', 'confidence': 0.9}]),
                 Jsonb([{'id': 'topic-1', 'name': '续航', 'keywords': ['续航'], 'doc_count': 1}]), '续航与价格'))
            sql('''INSERT INTO raw_documents(id,tenant_id,analysis_id,title,url,content,source_type,source_name,
                published_at,content_kind,final_url,semantic_classification,date_sources)
                VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)''',
                (docs[0]['id'], tenant_id, analysis_id, docs[0]['title'], docs[0]['url'], docs[0]['content'],
                 'toutiao', '今日头条', docs[0]['published_at'], 'body', docs[0]['url'], 'direct', Jsonb(docs[0]['date_sources'])))

            def add_report(report_id, format, version):
                sql('''INSERT INTO reports(id,tenant_id,analysis_id,format,status,file_key,created_by,report_version)
                       VALUES(%s,%s,%s,%s,'completed',%s,%s,%s)''',
                    (report_id, tenant_id, analysis_id, format, 'reports/' + report_id + '.' + format, user_id, version))

            add_report(html_id, 'html', 1)
            add_report(md_id, 'markdown', 2)
            add_report(pdf_id, 'pdf', 3)
            response = api('GET', '/reports/' + html_id + '/download?format=html', token)
            check('HTML download returns exact stored content', response.status_code == 200 and response.content == v1.encode(),
                  status=response.status_code, content_type=response.headers.get('Content-Type'))
            (EVIDENCE / 'v1-before.html').write_bytes(response.content)
            check('HTML attachment has the requested report id', html_id in response.headers.get('Content-Disposition', ''))
            response = api('GET', '/reports/' + html_id + '/download?format=html')
            check('unauthenticated HTML download denied', response.status_code == 401, status=response.status_code)
            for path in ['/reports/' + html_id, '/reports/' + html_id + '/download?format=html']:
                response = api('GET', path, other['access_token'])
                check('cross-tenant access denied ' + path.rsplit('/', 1)[-1], response.status_code in (403, 404), status=response.status_code)

            # A rerun mutates analyses.report_content and keeps the older report row.
            sql('UPDATE analyses SET report_content=%s WHERE id=%s', (v2, analysis_id))
            add_report(v2_id, 'html', 4)
            response = api('GET', '/reports/' + html_id + '/download?format=html', token)
            (EVIDENCE / 'v1-after-rerun.html').write_bytes(response.content)
            check('historical report id preserves its first snapshot', response.status_code == 200 and response.content == v1.encode(),
                  status=response.status_code, returned_v2=response.content == v2.encode())

            # The ReportHistoryPage advertises these format actions for every row.
            # Preserve the observed HTTP statuses as product acceptance evidence.
            sql("UPDATE report_credits SET plan_code='pro',plan_source='manual' WHERE tenant_id=%s", (tenant_id,))
            for fmt, report_id in [('md', html_id), ('pdf', html_id), ('docx', html_id)]:
                response = api('GET', '/reports/' + report_id + '/download?format=' + fmt, token)
                check('ReportHistoryPage advertised export is actionable: ' + fmt, response.status_code == 200,
                      status=response.status_code, envelope=response.json() if response.headers.get('Content-Type','').startswith('application/json') else {})
            # The user-facing report center advertises Word. Seed a matching row
            # to trace the native Go -> Python stream path and its actual JSON.
            add_report(word_id, 'docx', 3)
            response = api('GET', '/reports/' + word_id + '/download?format=docx', token)
            valid_zip = response.status_code == 200 and zipfile.is_zipfile(io.BytesIO(response.content))
            text = docx_text(response.content) if valid_zip else ''
            if valid_zip:
                (EVIDENCE / 'go-download.docx').write_bytes(response.content)
            check('native Word download contains valid DOCX bytes', valid_zip,
                  status=response.status_code, body=response.text[:800] if not valid_zip else '<valid DOCX>')
            request = wire[-1] if wire else {}
            received_docs = request.get('documents') or []
            check('Word request retains saved source documents', len(received_docs) == 1 and all(
                  received_docs[0].get(key) == docs[0][key] for key in ('id', 'content', 'url', 'content_kind')),
                  documents=request.get('documents'), context_documents=request.get('context_documents'))
            check('Word request retains dimensions and period', any(
                  row.get('findings') == dimensions[0]['findings'] for row in request.get('dimensions') or [])
                  and request.get('date_from') == '2026-10-01' and request.get('date_to') == '2026-10-20',
                  dimension_count=len(request.get('dimensions') or []), date_from=request.get('date_from'), date_to=request.get('date_to'))
            check('Word request retains scene and template', request.get('scenario_template_id') == 'product_launch'
                  and request.get('template_id') == 'event', scenario=request.get('scenario_template_id'), template=request.get('template_id'))
            # A direct engine control distinguishes a Go adapter failure from a DOCX library failure.
            control = transport.post(ENGINE + '/generate', headers={'X-Internal-Token': secret}, json={
                'title': 'QA完整分析名称', 'format': 'docx', 'documents': docs, 'context_documents': [],
                'dimensions': dimensions, 'date_from': '2026-10-01', 'date_to': '2026-10-20', 'template_id': 'event'})
            control_valid = control.status_code == 200 and zipfile.is_zipfile(io.BytesIO(control.content))
            control_text = docx_text(control.content) if control_valid else ''
            check('direct Python control creates a valid Word source appendix', control_valid and docs[0]['id'] in control_text
                  and docs[0]['url'] in control_text and 'QA_DIMENSION_FINDING' in control_text,
                  status=control.status_code, valid_docx=control_valid,
                  scope='no LLM key: rendering control only, not successful AI report acceptance')
            if control_valid:
                (EVIDENCE / 'python-control.docx').write_bytes(control.content)
            sql("UPDATE report_credits SET plan_code='lite' WHERE tenant_id=%s", (tenant_id,))
            response = api('GET', '/reports/' + word_id + '/download?format=docx', token)
            check('Lite native Word request is forbidden', response.status_code == 403, status=response.status_code)
            check('view and export did not consume report credits',
                  sql('SELECT count(*) FROM credit_transactions WHERE tenant_id=%s AND reason=\'consume\'', (tenant_id,))[0][0] == 0)
            check('no live supplier requests were authorized by export',
                  sql('SELECT count(*) FROM llm_call_authorizations')[0][0] == 0)
            (EVIDENCE / 'word-request.json').write_text(json.dumps(wire, ensure_ascii=False, indent=2))
            (EVIDENCE / 'engine-responses.json').write_text(json.dumps(engine_responses, ensure_ascii=False, indent=2))
    except Exception:
        check('audit infrastructure completed', False, error=traceback.format_exc())
    finally:
        for process in reversed(processes):
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        proxy.shutdown()
        proxy.server_close()
        transport.close()
        summary = {'source_sha': os.environ.get('GITHUB_SHA'),
                   'production_baseline': 'b3d1a5762160653084d5d96e36f81c5ab7ba68e5',
                   'result': 'FAIL' if any(not check['passed'] for check in CHECKS) else 'PASS',
                   'checks': CHECKS, 'live_supplier_calls': 0,
                   'scope': 'actual prebuilt Go HTTP API + PostgreSQL + real FastAPI/DOCX renderer; isolated fixtures'}
        (EVIDENCE / 'result.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2))
    return 1 if summary['result'] == 'FAIL' else 0


if __name__ == '__main__':
    raise SystemExit(main())

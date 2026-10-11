"""Prepare only disposable hosted-runner SDK loopback interception."""
import json
import os
import secrets
import subprocess
import tempfile
from pathlib import Path

assert os.environ.get('GITHUB_ACTIONS') == 'true'
assert os.environ.get('RUNNER_ENVIRONMENT') == 'github-hosted'
assert os.environ.get('RUNNER_OS') == 'Linux'
assert os.environ.get('YUQING_TEST_PG_URL') == 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable'
directory = Path(tempfile.mkdtemp(prefix='yuqing-notification-ci-', dir='/tmp'))
certificate = directory / 'certificate.pem'
key = directory / 'key.pem'
subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=yuqing-notification-ci', '-addext', 'subjectAltName=DNS:api.resend.com,DNS:dysmsapi.aliyuncs.com', '-keyout', str(key), '-out', str(certificate)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
(directory / 'control.json').write_text(json.dumps({'token': secrets.token_urlsafe(32)}))
(directory / 'control.json').chmod(0o600)
subprocess.run(['sudo', '-n', 'tee', '-a', '/etc/hosts'], input='\n127.0.0.1 api.resend.com dysmsapi.aliyuncs.com\n', text=True, check=True, stdout=subprocess.DEVNULL)
for public_port, fixture_port in [('443', '9443'), ('80', '9080')]:
    subprocess.run(['sudo', '-n', 'iptables', '-t', 'nat', '-I', 'OUTPUT', '1', '-d', '127.0.0.1', '-p', 'tcp', '--dport', public_port, '-j', 'REDIRECT', '--to-ports', fixture_port], check=True)
with Path(os.environ['GITHUB_ENV']).open('a') as output:
    output.write('YUQING_NOTIFICATION_SANDBOX_DIR=' + str(directory) + '\n')
print('Prepared SDK loopback TLS and private sandbox controller; no external delivery.')

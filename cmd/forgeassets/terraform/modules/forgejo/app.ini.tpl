; Forgejo configuration, rendered by terraform from [forge.services.forgejo].
;
; INSTALL_LOCK and DISABLE_REGISTRATION are not variables and never will be: an
; open forge is a posture change, not a preference. The trial proved the
; headless path works with both nailed down.
APP_NAME = ${site_name}
RUN_USER = ${user}
RUN_MODE = prod
WORK_PATH = /var/lib/forgejo

[server]
PROTOCOL = http
DOMAIN = ${domain}
HTTP_ADDR = 0.0.0.0
HTTP_PORT = ${http_port}
%{ if root_url != "" ~}
ROOT_URL = ${root_url}
%{ endif ~}
DISABLE_SSH = false
SSH_PORT = 22
SSH_DOMAIN = ${domain}
APP_DATA_PATH = /var/lib/forgejo/data
LFS_START_SERVER = true
OFFLINE_MODE = true

[database]
DB_TYPE = sqlite3
PATH = /var/lib/forgejo/data/forgejo.db
LOG_SQL = false

[repository]
ROOT = /var/lib/forgejo/data/forgejo-repositories
DEFAULT_BRANCH = main

[security]
INSTALL_LOCK = true
DISABLE_GIT_HOOKS = true
IMPORT_LOCAL_PATHS = false

[service]
DISABLE_REGISTRATION = true
REQUIRE_SIGNIN_VIEW = true
ENABLE_NOTIFY_MAIL = false
DEFAULT_KEEP_EMAIL_PRIVATE = true
DEFAULT_ALLOW_CREATE_ORGANIZATION = true

[openid]
ENABLE_OPENID_SIGNIN = false
ENABLE_OPENID_SIGNUP = false

[session]
PROVIDER = file

[log]
MODE = console
LEVEL = info
ROOT_PATH = /var/lib/forgejo/log

[cron.update_mirrors]
; Pull-mirrors keep a read-only copy of GitHub main fresh. Note the trial
; finding recorded in concepts/hosted-git-and-prs/forge-hosting.md: a MIRROR
; repo is repo-wide read-only, so PR-bearing repos must be normal repos whose
; main grove pushes. This schedule only serves the passive mirrors.
ENABLED = true
SCHEDULE = @every 1h

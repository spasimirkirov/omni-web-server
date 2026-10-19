# DevStack

A portable, Laragon-style local web server for PHP development on Windows:
**Apache + several PHP versions side by side + MySQL/MariaDB**, with automatic
`*.local` virtual hosts, controlled from a web dashboard or the command line.
Everything lives in one folder and is driven by a single `devstack.exe`.

## Features

- **Automatic vhosts.** Every folder in `www` becomes `http://<folder>.local`.
  Each project has an on/off toggle. With *Auto vhost* on, new folders are
  enabled as soon as they appear; with it off, they appear disabled.
- **Framework detection.** Laravel projects (an `artisan` file, or
  `laravel/framework` in `composer.json`) are served from `public/`
  automatically. Symfony, CodeIgniter 4 (`public/`), Yii/Craft (`web/`) and
  WordPress are detected too. You can override the document root of any
  project. Detection runs continuously, so `composer create-project` switches
  to `public/` as soon as `artisan` exists.
- **Per-project PHP versions.** Install as many PHP versions as you like (the
  latest releases, or any older version from the windows.php.net archives) and
  choose one per project. Each version runs its own pool of `php-cgi` workers.
- **PHP extension toggles.** Every extension found in a version's `ext` folder
  has a switch. Load-order dependencies are handled for you (for example,
  enabling `pdo_mysql` also loads `pdo` and `mysqlnd`). `php.ini` values such
  as `memory_limit` can be edited per version. Changes restart only that
  version's workers.
- **MySQL 8.4 or MariaDB 11.4.** The server is initialized on first start
  (user `root`, no password). The dashboard lists, creates and drops
  databases, changes the root password, and can install Adminer at
  `http://adminer.local`.
- **hosts file managed for you.** A marked block in
  `C:\Windows\System32\drivers\etc\hosts` is kept in sync. It contains both
  `name.local` and `www.name.local`. The rest of the file is never touched.
- **Wildcard DNS (optional).** A built-in DNS server answers every
  `*.local` name, including deep subdomains like `api.blog.local` or
  `tenant1.shop.local`, with `127.0.0.1`. Apache adds
  `ServerAlias *.<project>.local`, so subdomains reach the right project,
  which suits multi-tenant apps and subdomain routing. On Windows, DevStack
  adds a DNS Client NRPT rule (Name Resolution Policy Table) that sends only
  `.local` lookups to `127.0.0.1`; all other DNS is unchanged. While that
  rule is active, the hosts file no longer needs editing, so no more UAC
  prompts when you add projects.
- **Easy HTTPS.** One switch serves every site at `https://name.local`.
  DevStack creates a local certificate authority once and issues a
  certificate per site covering `name.local` and `*.name.local`, renewing it
  automatically. Clicking "Trust certificate" adds the CA to your Windows user
  certificate store: one confirmation dialog, no admin rights, no OpenSSL
  or mkcert. PHP's curl/openssl trust it too, so your app can call its own
  `https://` endpoints.
- **Easy custom vhosts.** "Add vhost…" serves any folder outside `www` under a
  name you choose.
- **CLI shims.** `bin\shims\php.cmd`, `php83.cmd`, … run the CLI with the same
  `php.ini` as the web server, so `php artisan` and Composer see the same
  extensions. "Add to my PATH" on the PHP tab registers the folder.
- **Supervision.** Crashed processes restart with backoff. Closing DevStack
  stops Apache, PHP and MySQL with it, using a Windows job object, so nothing
  is left orphaned.

## Quick start (Windows 10/11, x64)

1. Install the [Microsoft Visual C++ Redistributable (x64)](https://aka.ms/vs/17/release/vc_redist.x64.exe)
   if you don't have it. Apache, PHP and MySQL all need it, and the dashboard
   warns you when it is missing.
2. Unzip DevStack somewhere short without spaces, e.g. `C:\DevStack`.
3. Right-click `devstack.exe` → **Run as administrator**. Admin rights are only
   needed to edit the hosts file. Without them, you get a UAC prompt each time
   the hosts file has to change.
4. The dashboard opens at <http://localhost:8800>. Install the components:
   - **Overview → Install Apache**: the latest Apache Lounge build.
   - **PHP → Install version…**: pick 8.4, 8.3, … or type e.g. `7.4.33`.
   - **Overview → Install MySQL 8.4** (or MariaDB 11.4).
5. Click **Start all**. A sample project is at <http://welcome.local>.

Create a Laravel app and it is live right away:

```bat
cd C:\DevStack\www
composer create-project laravel/laravel blog
:: -> http://blog.local (DocumentRoot = www\blog\public)
```

`.env` for MySQL: `DB_HOST=127.0.0.1`, `DB_PORT=3306`, `DB_USERNAME=root`,
`DB_PASSWORD=` (empty unless you changed it). Create the database on the
Databases tab or with `devstack db create blog`.

### When an automatic download fails

The download sites change from time to time. Each install button has a
**From URL…** option that takes any `.zip` URL. You can also unzip a download
yourself:

| Component | Unzip into | DevStack looks for |
|-----------|------------|--------------------|
| Apache (apachelounge.com, Win64) | `bin\apache\<any name>\` | `bin\httpd.exe` |
| PHP (windows.php.net, **NTS x64**) | `bin\php\<any name>\` | `php-cgi.exe` |
| MySQL / MariaDB (ZIP archive, winx64) | `bin\mysql\<any name>\` | `bin\mysqld.exe` |

Restart DevStack and the new folders are registered automatically.

### Wildcard DNS

Turn it on under **Settings → Wildcard DNS** or with `devstack dns on`.
Accept the one-time UAC prompt, which installs the NRPT rule. Check it from
PowerShell:

```powershell
Get-DnsClientNrptRule | Where-Object Comment -eq DevStack   # Namespace .local -> 127.0.0.1
Resolve-DnsName anything.blog.local                         # 127.0.0.1 / ::1
```

Notes:
- Windows can only send NRPT queries to port 53. If another program (e.g.
  Internet Connection Sharing) already uses `127.0.0.1:53`, DevStack shows
  that program. In that case sites keep working through the hosts file.
- `.local` is also used by mDNS (printers, network devices). If your network
  has `.local` devices, switch the domain suffix to `.test`, which is reserved
  for exactly this purpose; the rule follows automatically.
- Browsers using their own "secure DNS" (DNS-over-HTTPS) may bypass the
  system resolver. Chrome and Edge fall back to the system resolver when NRPT
  rules exist. In Firefox, add the suffix to
  `network.trr.excluded-domains` or turn off DoH.
- Turning wildcard DNS off removes the NRPT rule and restores the hosts
  entries. The rule stays in place while DevStack is closed. Apache is down
  then anyway, so nothing else is affected.
- On Linux/macOS, DevStack runs the DNS server but doesn't change the system
  resolver. Point dnsmasq (`server=/local/127.0.0.1`) or systemd-resolved at
  it yourself.

### HTTPS

Turn it on under **Settings → HTTPS** (or `devstack ssl on`), then click
**Trust certificate** (`devstack ssl trust`) and confirm the Windows dialog.
Every enabled site now also answers on `https://` with a valid certificate,
including one level of subdomains (`https://api.blog.local`). Plain `http://`
keeps working.

- Laravel: set `APP_URL=https://blog.local`. PHP receives `HTTPS=on`, so
  `url()` and `asset()` generate `https://` links.
- Phones and other devices: **Download CA** and install it there.
- The CA's private key is `etc\ssl\devstack-ca.key`. Anyone holding it can
  create certificates your PC trusts, so never share or commit it. To rotate
  it, delete `etc\ssl\devstack-ca.*`, then trust the new CA. Remove the old
  one from `certmgr.msc` → Trusted Root Certification Authorities.

## Command line

Run `devstack help` for the full list. Most commands talk to the running
DevStack (start it with `devstack start` or by double-clicking the exe).

```text
devstack start                      start DevStack in the background + all services
devstack stop | quit                stop services | stop services and exit
devstack status                     services, PHP pools and sites
devstack sites                      list projects and their vhosts
devstack site enable|disable blog   toggle the vhost
devstack site php blog 8.2          run blog on PHP 8.2 (use "default" to reset)
devstack site root blog web         custom document root (use "auto" to reset)
devstack site add shop D:\work\shop add a vhost for a folder outside www
devstack php install 8.4            download a PHP version (or 7.4.33 from the archives)
devstack php default 8.4            default PHP for projects without their own setting
devstack ext 8.4 on xdebug intl     enable extensions (off to disable)
devstack ini 8.4 memory_limit=1G    set php.ini values
devstack install mariadb            download MariaDB (also: apache, mysql, adminer)
devstack db create blog             create a utf8mb4 database
devstack dns on                     wildcard DNS: *.local (any depth) -> this PC
devstack ssl on | ssl trust         HTTPS for all sites | trust the local CA
devstack logs apache_error 100      show a log
```

## Folder layout

```text
DevStack\
  devstack.exe        the whole tool: dashboard, CLI, supervisor
  devstack.json       settings and per-project choices
  www\                your projects -> <name>.local
  bin\apache\…        Apache
  bin\php\php-8.4.x\  one folder per PHP version
  bin\mysql\…         MySQL or MariaDB
  bin\shims\          php.cmd, php84.cmd, … for the terminal
  etc\apache\httpd.conf     generated; put your own directives in etc\apache\custom\*.conf
  etc\ssl\             local CA, per-site certificates, CA bundle for PHP
  etc\php\8.4\php.ini       generated per version from php.ini-development + your settings
  etc\mysql\my.ini          generated; put overrides in etc\mysql\custom.ini
  data\mysql\         database files
  logs\               apache_error, apache_access, mysql_error, php84_error, devstack, …
```

Generated files are rewritten when settings change, so make changes in the
dashboard, `devstack.json`, or the `custom` include files.

## How it works

- **Apache** runs from a generated `httpd.conf` with one `<VirtualHost>` per
  enabled project and `AllowOverride All`, so `.htaccess` files (Laravel's
  rewrite rules) work. Requests for `.php` files that exist are handed to
  PHP over FastCGI with `SetHandler "proxy:fcgi://…"`. Dotfiles such as
  `.env` and `.git` are denied.
- **PHP** runs as a pool of `php-cgi.exe -b` workers per version (4 by
  default). php-cgi on Windows serves one request at a time, so DevStack
  listens on one port per version (`9000 + major*100 + minor*10`, e.g. 9840
  for 8.4). It hands each request to an idle worker and queues the rest.
  Each version can therefore run several requests at once, and versions are
  fully isolated from each other.
- A watcher rescans `www` every 3 seconds. It rewrites configuration only when
  something changed, and restarts Apache only after `httpd -t` accepts the new
  config.
- The dashboard listens on `127.0.0.1` only. It rejects requests with a
  foreign `Host` header (DNS rebinding) and state-changing requests without
  its own header (cross-site requests).

## Troubleshooting

- **"Port 80 is already in use by PID 4 System"**: IIS or the *World Wide Web
  Publishing Service* is running. Stop it in `services.msc`, or change the
  Apache port under Settings.
- **"port 80 is already in use by … Skype/other"**: close that program or
  change the port.
- **Apache/PHP/MySQL exits immediately**: install the VC++ Redistributable
  (step 1), then check the log the dashboard shows.
- **`name.local` doesn't resolve**: click *Settings → Rewrite hosts file now*
  (or *Apply DNS rule* when wildcard DNS is on) and accept the UAC prompt,
  or run DevStack as administrator. Then check that the DevStack block exists
  in the hosts file, or that `Get-DnsClientNrptRule` lists the DevStack rule.
- **Windows SmartScreen warns about devstack.exe**: the build is not
  code-signed. Choose *More info → Run anyway*.

## Building from source

Go 1.22+ and no third-party dependencies:

```sh
GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o dist/devstack.exe .
go test ./...
```

DevStack also runs on Linux. On first run it detects the system Apache
(`apache2`/`httpd`), every `php-cgi*` binary, and `mysqld`/`mariadbd`. The
end-to-end tests of this project were run that way.

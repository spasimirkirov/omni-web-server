# Credits

Omni Web Server is a small Go program that installs, configures and runs other
people's excellent software. None of the components below is bundled with
Omni Web Server. Each one is downloaded from its official source only when you
choose to install it, and it stays under its own license.

## Made by

- **[Spasimir Kirov](https://github.com/spasimirkirov)**: author, product
  design and architecture.
- Developed with **[Claude Code](https://claude.com/claude-code)**, Anthropic's
  AI coding agent.

## Server components

| Component | Downloaded from | License |
|---|---|---|
| [Apache HTTP Server](https://httpd.apache.org/) | Windows builds by [Apache Lounge](https://www.apachelounge.com/) | Apache License 2.0 |
| [PHP](https://www.php.net/) | [windows.php.net](https://windows.php.net/) | PHP License 3.01 |
| [MySQL Community Server](https://www.mysql.com/) | [dev.mysql.com](https://dev.mysql.com/downloads/mysql/) | GPL 2.0 |
| [MariaDB](https://mariadb.org/) | [mariadb.org](https://mariadb.org/download/) | GPL 2.0 |
| [Redis](https://redis.io/) | Windows builds by [redis-windows](https://github.com/redis-windows/redis-windows) | the Redis license of the downloaded version (RSALv2 / SSPLv1 / AGPLv3) |
| [Mailpit](https://mailpit.axllent.org/) | [GitHub releases](https://github.com/axllent/mailpit/releases) | MIT |
| [Composer](https://getcomposer.org/) | [getcomposer.org](https://getcomposer.org/) | MIT |
| [Node.js](https://nodejs.org/) | [nodejs.org](https://nodejs.org/) | MIT |
| [Xdebug](https://xdebug.org/) | [xdebug.org](https://xdebug.org/download) | Xdebug License 3.0 |
| [Adminer](https://www.adminer.org/) | [adminer.org](https://www.adminer.org/) | Apache License 2.0 or GPL 2.0 |
| [Mozilla CA certificate bundle](https://curl.se/docs/caextract.html) | [curl.se](https://curl.se/ca/cacert.pem) | MPL 2.0 |
| [Microsoft Visual C++ Redistributable](https://learn.microsoft.com/cpp/windows/latest-supported-vc-redist) | [aka.ms](https://aka.ms/vs/17/release/vc_redist.x64.exe) | Microsoft Software License Terms |

## Optional developer apps

The **Tools** tab can install these through
[winget](https://learn.microsoft.com/windows/package-manager/). Each comes
from its publisher under its own terms:

- [Git for Windows](https://gitforwindows.org/)
- [Windows Terminal](https://aka.ms/terminal)
- [Visual Studio Code](https://code.visualstudio.com/)
- [PhpStorm](https://www.jetbrains.com/phpstorm/)
- [HeidiSQL](https://www.heidisql.com/)
- [DBeaver](https://dbeaver.io/)
- [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
- [ngrok](https://ngrok.com/)
- [Docker Desktop](https://www.docker.com/products/docker-desktop/)
- [WSL](https://learn.microsoft.com/windows/wsl/)

## Build tools

- [Go](https://go.dev/) (BSD 3-Clause). Omni Web Server uses only the standard library.
- [NSIS](https://nsis.sourceforge.io/) (zlib/libpng) builds the installer.
- [go-winres](https://github.com/tc-hib/go-winres) (MIT) embeds the icon and version information.

## Other projects

[XAMPP](https://www.apachefriends.org/) and [Laragon](https://laragon.org/)
inspired this project. Omni Web Server is not affiliated with either. It only
reads their files when you ask it to import your projects.

All product names and trademarks belong to their owners.

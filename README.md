# SIIASQL — MCP de SIIA

`siiasql` es el servidor MCP de SIIA. Expone una superficie de **consultas SQL de solo
lectura** sobre SER v3.0, más herramientas de introspección para descubrir el esquema. Habla
MCP por `stdio` y, opcionalmente, por Streamable HTTP; impone un guard de AST de solo lectura y
límites de recursos, y mantiene las credenciales fuera de logs, auditoría y respuestas MCP.

No ejecuta escritura ni DDL: cada sentencia pasa por un guard de AST que solo admite un
`SELECT` de solo lectura. El alcance de objetos alcanzables se acota con la lista de permisos
de la conexión y con los permisos reales del rol PostgreSQL.

## Inicio Rápido

Esta es la ruta recomendada. SIIASQL no tiene asistente interactivo: `init` crea la base y tú
completas el archivo de entorno con los secretos.

1. Instala la última release:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/ofepbolivia/siia-mcp/main/scripts/install.sh | sh
   ```

2. Crea la configuración base y el archivo de secretos:

   ```bash
   siiasql init
   ```

   Crea `~/.config/siiasql/config.yaml`, `~/.config/siiasql/env` (permisos `0600`) y el sink de
   auditoría. No sobrescribe archivos existentes.

3. Completa los secretos en `~/.config/siiasql/env`:

   ```sh
   SIIA_DB_HOST=127.0.0.1
   SIIA_DB_DATABASE=mydb
   SIIA_DB_USER=readonly_user
   SIIA_DB_PASSWORD=...
   ```

   Las contraseñas no deben contener espacios. El archivo de entorno debe quedar con permisos
   `0600` o más estrictos.

4. Ajusta `~/.config/siiasql/config.yaml` a tu entorno: `host`, `port` y `allow.schemas`. Si el
   host no es un socket Unix, loopback o IP privada literal, `tls.mode` debe ser `verify-full`.

5. Verifica el setup:

   ```bash
   siiasql doctor
   ```

   Comprueba que la config y el entorno cargan, que el sink de auditoría es escribible y que la
   conexión responde a `db_ping`.

## Requisitos

| Requisito | Nota |
|-----------|------|
| Go | `go.mod` declara Go `1.25.0`. |
| CGO | Requerido por las dependencias de parseo/encoding fijadas. |
| PostgreSQL | Única fuente soportada: SER v3.0. Los tests de integración corren contra PostgreSQL 15-18. |
| Rol de solo lectura | Obligatorio. La validación interna no sustituye los permisos reales de PostgreSQL. |

## Principios

- **Solo lectura.** No existen operaciones de escritura ni DDL.
- **SQL de solo lectura permitido.** `db_query` y `db_explain` aceptan SQL parametrizado; el
  guard de AST rechaza cualquier cosa que no sea un `SELECT` único.
- **Introspección disponible.** Se pueden listar esquemas, tablas, columnas, índices y
  relaciones para construir consultas.
- **Alcance acotado.** `allow.schemas`/`allow.views` restringe los objetos alcanzables; el rol
  PostgreSQL de solo lectura es la frontera base.
- **Límites de recursos.** Tiempo, filas, tamaño de valor, tamaño de respuesta y concurrencia
  tienen techos internos configurables.
- **Trazabilidad.** Auditoría obligatoria y fail-closed, sin registrar SQL, parámetros ni datos.

## Instalación

Usa esta sección solo si necesitas una versión fija, verificación manual del archivo o
compilación desde código fuente. Si ya completaste el Inicio Rápido, puedes omitirla.

Instalación recomendada desde GitHub Releases:

```bash
curl -fsSL https://raw.githubusercontent.com/ofepbolivia/siia-mcp/main/scripts/install.sh | sh
```

Instalar una versión específica:

```bash
SIIASQL_VERSION=v0.1.0 sh -c "$(curl -fsSL https://raw.githubusercontent.com/ofepbolivia/siia-mcp/main/scripts/install.sh)"
```

El instalador detecta SO/arquitectura, descarga el tarball correspondiente, verifica
`checksums.txt` e instala `siiasql` en `/usr/local/bin` o en `~/.local/bin` cuando
`/usr/local/bin` no es escribible.

Para espejos o pruebas de release, define `SIIASQL_DOWNLOAD_BASE` con la URL del directorio de
assets que contiene el tarball de la plataforma y `checksums.txt`.

Instalación manual:

1. Descarga el archivo de tu plataforma desde [GitHub Releases](https://github.com/ofepbolivia/siia-mcp/releases).
2. Descarga `checksums.txt` de la misma release.
3. Verifica el archivo:

   ```bash
   ARCHIVE=siiasql_linux_amd64.tar.gz
   grep "  $ARCHIVE$" checksums.txt | sha256sum -c -
   # alternativa en macOS:
   grep "  $ARCHIVE$" checksums.txt | shasum -a 256 -c -
   ```

4. Extrae y mueve `siiasql` a un directorio en `PATH`.

Compilación desde código fuente:

```bash
git clone https://github.com/ofepbolivia/siia-mcp.git
cd siia-mcp
make build            # genera bin/siiasql
# o
go build -o bin/siiasql ./cmd/siiasql
```

Prefiere una ruta explícita (`bin/siiasql`) para lanzar el servidor.

## Comandos

| Comando | Propósito |
|---------|-----------|
| `siiasql --config <path>` | Inicia el servidor MCP; por `stdio` o, con `--http <addr>`, sobre Streamable HTTP. |
| `siiasql init [--config <path>]` | Crea la configuración base, el sink de auditoría y el esqueleto de secretos. |
| `siiasql doctor [--config <path>]` | Verifica configuración, entorno, sink de auditoría y salud de la conexión. |

`siiasql --config <path>` no es un comando interactivo: espera mensajes del protocolo MCP en
`stdin` y escribe las respuestas en `stdout`. Con `--http <addr>` no lee `stdin`: escucha
Streamable HTTP en `POST <addr>/mcp` y sigue registrando en `stderr`. Al ejecutarlo manualmente
registra `server.ready` en `stderr` y queda esperando mensajes MCP.

### Rutas por defecto

Cuando se omite `--config`, SIIASQL usa:

- Config: `~/.config/siiasql/config.yaml` (vía `XDG_CONFIG_HOME` si está definido).
- Secretos: `~/.config/siiasql/env` (se puede cambiar con `SIIASQL_ENV`).
- Auditoría: `~/.local/state/siiasql/audit.jsonl` (vía `XDG_STATE_HOME` si está definido).

La configuración y el archivo de entorno se escriben con permisos `0600`.

## Servidor HTTP (Streamable HTTP)

`stdio` es el transporte por defecto. Para exponer el mismo servidor a un cliente que habla
Streamable HTTP, arranca con `--http <addr>`. Esta es la ruta para quien clona el repositorio y
quiere levantar el servidor localmente.

### Requisitos

- Go `1.25.0`, la versión que declara `go.mod`, con CGO habilitado.
- Docker o Podman, solo si vas a usar la base de pruebas opcional (ver más abajo).

### 1. Compilar

```bash
make fast             # gofmt, vet, tests, race y build — genera bin/siiasql
# o solo compilar el binario:
make build
```

### 2. Crear la configuración

```bash
./bin/siiasql init --config ./config.local.yaml
```

`init` crea `./config.local.yaml`, el sink de auditoría
(`~/.local/state/siiasql/audit.jsonl`) y el esqueleto de secretos en `~/.config/siiasql/env`
con permisos `0600`. No sobrescribe archivos existentes. Tanto `./config.local.yaml` como el
archivo de entorno son locales: no los subas al repositorio.

### 3. Completar los secretos

Edita `~/.config/siiasql/env`:

```sh
SIIA_DB_HOST=127.0.0.1
SIIA_DB_DATABASE=mydb
SIIA_DB_USER=readonly_user
SIIA_DB_PASSWORD=...
```

`connection.port` es un valor literal en la configuración (por defecto `5432`); si tu base
escucha en otro puerto, cámbialo en `config.local.yaml`, no en el archivo de entorno.

### 4. Base de pruebas opcional

Si no tienes una base a mano, `scripts/it-pg.sh` levanta un PostgreSQL desechable y, con
`KEEP_CONTAINER=1`, lo deja corriendo:

```bash
KEEP_CONTAINER=1 scripts/it-pg.sh 16
```

Queda en `127.0.0.1:55432` con usuario `ituser`, clave `itpass` y base `itdb` (credenciales
solo del contenedor desechable). Con esa base, ajusta `connection.port` a `55432` en
`config.local.yaml`. Para limpiarlo:

```bash
docker rm -f siiasql-it-16    # o: podman rm -f siiasql-it-16
```

### 5. Validar

```bash
./bin/siiasql doctor --config ./config.local.yaml
```

Debe terminar con `connection siia_ser: ok`.

### 6. Arrancar en HTTP

```bash
./bin/siiasql --config ./config.local.yaml --http 127.0.0.1:8000
```

En `stderr` verás `listening over Streamable HTTP`. El endpoint es
`POST http://127.0.0.1:8000/mcp`.

### 7. Probar con curl

El servidor no usa sesiones: cada request es independiente y no necesitas mantener un
`Mcp-Session-Id`. `initialize` negocia `protocolVersion`:

```bash
curl -sS -X POST http://127.0.0.1:8000/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'
```

Y una consulta de solo lectura vía `db_query`:

```bash
curl -sS -X POST http://127.0.0.1:8000/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"db_query","arguments":{"connection":"siia_ser","sql":"SELECT 1 AS ok"}}}'
```

Cualquier sentencia que no sea un único `SELECT` de solo lectura se rechaza con
`QUERY_REJECTED`.

## Configuración

SIIASQL requiere una ruta de configuración explícita con `--config <path>`. No busca archivos
implícitos, y los valores sensibles no se pueden sobrescribir por flags.

Usa interpolación de entorno para los secretos. Un valor como `${SIIA_DB_PASSWORD}` se resuelve
primero del entorno del proceso y luego del archivo de entorno de SIIASQL. No confirmes
contraseñas ni DSNs en el repositorio; guárdalas en el archivo de entorno, que se escribe con
permisos `0600`.

La `audit.path` del ejemplo es una ruta absoluta literal. `siiasql init` la genera
automáticamente; el parser no expande `~`, así que escríbela completa o usa una `${VAR}`.

```yaml
version: 1

server:
  request_timeout: 15s
  shutdown_timeout: 10s
  active_requests: 4
  queued_requests: 4
  queue_timeout: 2s
  max_total_pool_connections: 16

limits:
  mcp_frame_bytes: 4194304
  json_nesting_depth: 32
  query_bytes: 32768
  parameter_count: 64
  parameter_bytes: 262144
  parameters_total_bytes: 1048576
  query_rows: 200
  sample_rows: 20
  value_bytes: 262144
  response_bytes: 2097152
  metadata_page_size: 100

metadata_cache:
  ttl: 60s
  max_entries: 1024
  max_bytes: 16777216

logging:
  level: info
  sink: stderr

audit:
  sink: file
  path: /home/you/.local/state/siiasql/audit.jsonl
  write_timeout: 2s
  queue_size: 64
  max_file_bytes: 268435456
  max_files: 8

network:
  intranet_cidrs: []

# SIIASQL se conecta con una única conexión de solo lectura a SER v3.0.
connection:
  name: app_ro
  engine: postgres
  mode: readonly
  required: true
  initialize: eager
  host: ${SIIA_DB_HOST}
  port: 5432
  database: ${SIIA_DB_DATABASE}
  user: ${SIIA_DB_USER}
  password: ${SIIA_DB_PASSWORD}
  tls:
    mode: disable
  pool:
    max_connections: 4
    min_connections: 0
    max_connection_lifetime: 30m
    max_connection_idle_time: 5m
    health_check_period: 30s
  allow:
    schemas:
      - public
      - reporting
```

### Reglas importantes

| Área | Regla |
|------|-------|
| Transporte | `stdio` por defecto; Streamable HTTP opcional con `--http <addr>` (endpoint `POST /mcp`). |
| Motor | Solo PostgreSQL en v1, con una **única conexión** (`connection`). |
| Modo | Solo `readonly`. |
| SQL | Una sentencia por request. Se rechazan escrituras, DDL, CTEs con escritura y multi-sentencias. |
| Credenciales | Interpolación de entorno. Las contraseñas van en el archivo de entorno, no en la config. No se devuelven, registran, auditan ni confirman. |
| TLS | `disable` solo se admite para sockets Unix, hosts loopback o IP privadas literales. Hostnames e IPs públicas requieren `verify-full`. |
| Allowlist | `allow` es opcional. Si se omite, el alcance es el que conceda el rol PostgreSQL; si se define, es una lista de permiso por defecto (deny por defecto). Los nombres son identificadores exactos, no globs ni regex. |

`host`, `database`, `user` y `password` se resuelven del archivo de entorno mediante `${VAR}`
(`SIIA_DB_HOST`, `SIIA_DB_DATABASE`, `SIIA_DB_USER`, `SIIA_DB_PASSWORD`); `port` queda literal.

## Herramientas MCP

Consulta:

| Herramienta | Propósito |
|-------------|-----------|
| `db_query` | Ejecuta un `SELECT` de solo lectura parametrizado. |
| `db_explain` | Devuelve el plan de ejecución permitido (sin `ANALYZE`). |

Descubrimiento de esquema:

| Herramienta | Propósito |
|-------------|-----------|
| `db_list_connections` | Lista las conexiones configuradas sin exponer credenciales. |
| `db_ping` | Comprueba que una conexión responde. |
| `db_list_schemas` | Lista los esquemas autorizados. |
| `db_list_tables` | Lista tablas y vistas de un esquema. |
| `db_describe_table` | Describe columnas, tipos, claves y restricciones. |
| `db_list_indexes` | Lista los índices de una tabla. |
| `db_list_relationships` | Lista las claves foráneas visibles. |
| `db_suggest_relationships` | Sugiere joins lógicos por columnas homónimas sin FK declarada. |
| `db_search_columns` | Busca columnas por patrón de nombre. |
| `db_find_references` | Busca columnas cuyos valores coinciden con un valor dado. |
| `db_sample_rows` | Devuelve una muestra acotada de filas. |
| `db_count_rows` | Devuelve el conteo exacto o estimado de filas. |

Cada respuesta de `db_query` devuelve columnas y filas; los valores `NUMERIC`/`DECIMAL` se
serializan como cadenas para preservar la precisión exacta.

## Seguridad

- Use un rol PostgreSQL genuinamente de solo lectura; el guard y la validación son defensa en
  profundidad.
- No se devuelven ni auditan credenciales, DSNs, SQL, parámetros ni valores de resultado.
- `NUMERIC`/`DECIMAL` se serializan como cadenas para preservar precisión exacta.
- La auditoría es fail-closed: si no puede confirmarse su entrega, el servidor deja de admitir
  operaciones.
- No configures un proceso para múltiples dominios de confianza. Distintos usuarios o fronteras
  necesitan procesos, configs y roles PostgreSQL separados.

## Releases

Las releases de producción se publican desde tags Git (`v*`) por
`.github/workflows/release.yml`. Cada release incluye:

- `siiasql_linux_amd64.tar.gz`
- `siiasql_linux_arm64.tar.gz`
- `siiasql_darwin_amd64.tar.gz`
- `siiasql_darwin_arm64.tar.gz`
- `checksums.txt`

Como SIIASQL usa CGO, los binarios se construyen en runners nativos de GitHub Actions en lugar
de depender de compilación cruzada desde un solo host.

## Verificación

```bash
make fast        # gofmt, vet, tests, race, build
make e2e         # pruebas E2E MCP a nivel de protocolo
make it          # pruebas de integración (Docker o Podman; PG 15 y 18)
make it-matrix   # matriz completa PostgreSQL 15-18
make fuzz        # fuzz smoke acotado
```

Los tests de integración levantan un contenedor PostgreSQL efímero vía
`scripts/it-pg.sh`, siembran `scripts/fixtures/it.sql` y exponen `SIIASQL_IT_*`
automáticamente.

## Documentación

- [`docs/siiasql-prd.md`](docs/siiasql-prd.md) — requisitos de producto.
- [`docs/siiasql-sdd.md`](docs/siiasql-sdd.md) — diseño de software y contrato de consulta.
- [`docs/user-stories/`](docs/user-stories/) — historias de usuario.
- [`docs/README.md`](docs/README.md) — índice de documentación.

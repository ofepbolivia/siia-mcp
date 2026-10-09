# SDD — SIIASQL MCP (MCP de SIIA)

**Producto:** SIIASQL — MCP de SIIA
**Binario:** `siiasql`
**Estado:** Diseño alineado con SIIA
**Versión:** 2.0
**Fuente de requisitos:** `siiasql-prd.md` v2.0 y `../../docs/siia-sdd.md`

---

# 1. Propósito

Este documento define cómo implementar SIIASQL sin reabrir ni ampliar las decisiones de producto
del PRD. Es normativo para arquitectura, superficie de herramientas, guard de solo lectura,
autorización, configuración, límites, concurrencia, auditoría y verificación.

**debe**, **no debe** y **sólo** expresan requisitos obligatorios. Los valores marcados como
*default* podrán ajustarse después de medir; los techos internos no podrán elevarse mediante
configuración o requests sin una nueva revisión de diseño y seguridad.

## 1.1 Resultado esperado

Un binario local escrito en Go que:

- atiende MCP exclusivamente por `stdio`;
- expone una superficie de consultas SQL de solo lectura parametrizadas, más introspección de
  esquema;
- rechaza toda sentencia que no sea un `SELECT` único de solo lectura;
- acota los objetos alcanzables por lista de permisos y grants del rol;
- aplica límites, cancelación y auditoría fail-closed;
- devuelve resultados estructurados, completos y sin pérdida silenciosa de precisión.

# 2. Decisiones arquitectónicas

| Tema | Decisión | Motivo |
|---|---|---|
| Rol del producto | MCP consumido por el orquestador | Es la frontera aprobada por SIIA. |
| Transporte | SDK oficial MCP Go sobre `stdio` | Único transporte v1; el proceso local es el principal de confianza. |
| Superficie | Consulta SQL de solo lectura + introspección | Sostiene la consulta libre con un guard de solo lectura. |
| Guard | Parser de AST PostgreSQL; un `SELECT` único | Rechazo fail-closed de escritura, DDL y bloqueos. |
| Fuente de datos | SER v3.0 sobre una conexión de solo lectura | Fuente única. |
| Driver | `pgx/v5` y `pgxpool` | Lifecycle, cancelación y tipos PostgreSQL. |
| Autorización de usuario | Resuelta en la API; el MCP ejecuta con su rol y lista de permisos | Defensa en capas. |
| Resultados | `structuredContent` versionado con columnas tipadas y filas posicionales | Preserva orden, precisión y tamaño compacto. |
| Auditoría | Sink separado, serializado, confirmado y fail-closed | La trazabilidad es un invariante de release. |
| Configuración | YAML estricto, inmutable, con interpolación de entorno | Sin defaults ambiguos ni recargas inseguras. |
| Motores | PostgreSQL es el único motor | SER v3.0 es la única fuente. |

# 3. Arquitectura

## 3.1 Vista general

```text
stdin
  |
  v
MCP SDK -> bounded ingress -> audit attempt -> admission -> input validation
                                                    |
                                                    v
                                        connection lookup + allowlist
                                                    |
                                                    v
                                 AST guard (single read-only SELECT)
                                                    |
                                                    v
                                     parameter binding + read-only tx
                                                    |
                                                    v
                                         PostgreSQL (rol de solo lectura)
                                                    |
                                                    v
                                          bounded result encoder
                                                    |
                        +---------------------------+
                        v
                 audit outcome -> MCP structured result -> stdout

logs --------------------------------------------------------------> stderr
audit -----------------------------------------------------> dedicated file/sink
```

## 3.2 Camino crítico de una consulta

1. routing de la herramienta;
2. reserva acotada de ingress;
3. auditoría del intento;
4. admisión global;
5. validación de entrada, parámetros y límites efectivos;
6. lookup de la conexión y su lista de permisos;
7. guard de AST sobre el `SELECT`;
8. adquisición del pool;
9. transacción de solo lectura;
10. ejecución e iteración;
11. encoding acotado;
12. rollback, reset y liberación;
13. auditoría del resultado;
14. respuesta MCP.

No se agregan capas repository/service, buses internos ni abstracciones por motor dentro de este
camino sin duplicación medida que las justifique.

## 3.3 Componentes

| Componente | Responsabilidad |
|---|---|
| Tool registry | Catálogo estable y versionado de herramientas y sus esquemas de entrada. |
| Guard | Parseo y validación de AST: un `SELECT` único de solo lectura. |
| Param binder | Enlaza parámetros tipados posicionales; nunca interpola texto. |
| Query executor | Ejecuta la consulta en transacción de solo lectura con deadline derivado. |
| Metadata provider | Introspección de esquemas, tablas, columnas, índices y relaciones. |
| Result encoder | Serializa columnas y filas sin pérdida, con límites. |
| Metadata cache | Cache acotada de metadatos con TTL. |
| Admission gate | Slots activos y cola acotada. |
| Audit sink | Entrega serializada, confirmada y fail-closed. |
| Connection registry | Handles por nombre lógico y pools reutilizados. |

# 4. Superficie de herramientas

## 4.1 Registro

Cada herramienta declara nombre, descripción, esquema de entrada cerrado y códigos de error. El
registro es inmutable durante el proceso y se cubre con golden files para que el contrato de
cable no derive de los tipos Go.

## 4.2 Entrada

- JSON object únicamente; campos desconocidos se rechazan.
- Las herramientas de consulta reciben `connection`, `sql` y `parameters` tipados.
- Las herramientas de metadatos reciben filtros de objeto (`schema`, `table`, `pattern`) y
  paginación opcional (`limit`, `cursor`).
- `limits` permite reducir timeout, filas y bytes de respuesta; nunca ampliarlos.

## 4.3 Envelope de éxito

```json
{
  "schema_version": "1",
  "data": {
    "columns": [{ "name": "empresa", "postgres_type": "text", "type_oid": 25, "format": "text", "sensitive": false }],
    "rows": [["ACME"]],
    "row_count": 1
  },
  "meta": { "duration_ms": 12, "truncated": false, "truncation_reason": null }
}
```

El resultado completo aparece una sola vez en `structuredContent`; `content` contiene solo una
frase constante sin valores.

## 4.4 Envelope de error

```json
{
  "schema_version": "1",
  "error": {
    "code": "QUERY_REJECTED",
    "message": "only SELECT statements are allowed",
    "retryable": false,
    "request_id": "9f4c..."
  }
}
```

No se devuelven errores raw del driver, stack traces, SQL, parámetros, DSNs, hosts, usuarios ni
nombres de objetos fuera de alcance.

## 4.5 Códigos públicos

| Código | Retryable | Uso |
|---|---:|---|
| `INVALID_REQUEST` | no | Esquema, campo o límite inválido. |
| `INVALID_CURSOR` | no | Cursor corrupto, vencido o de otro contexto. |
| `CONNECTION_NOT_FOUND` | no | Nombre de conexión no configurado. |
| `CONNECTION_UNAVAILABLE` | sí | Handle opcional/lazy no disponible. |
| `OBJECT_NOT_ALLOWED` | no | Referencia a un objeto fuera de la lista de permisos. |
| `OBJECT_NOT_FOUND` | no | Objeto solicitado inexistente. |
| `QUERY_REJECTED` | no | La sentencia no es un `SELECT` único de solo lectura. |
| `PARAMETER_MISMATCH` | no | Conteo o tipo de parámetros inconsistente con la sentencia. |
| `INPUT_LIMIT_EXCEEDED` | no | Entrada o parámetros exceden límites. |
| `RESULT_VALUE_TOO_LARGE` | no | Una celda completa supera el techo. |
| `TIMEOUT` | sí | Deadline efectivo agotado. |
| `CANCELLED` | sí | Cancelación del cliente o shutdown. |
| `SERVER_BUSY` | sí | Capacidad activa/cola agotada. |
| `METADATA_UNAVAILABLE` | sí | Introspección no disponible. |
| `AUDIT_UNAVAILABLE` | sí | No pudo confirmarse entrega de auditoría. |
| `DATABASE_ERROR` | no | Error PostgreSQL sanitizado. |
| `INTERNAL_ERROR` | no | Invariante o error inesperado. |

La precedencia es: cancelación explícita, deadline, audit failure, input/guard, database e
internal.

# 5. Guard de solo lectura

El guard parsea la sentencia con el parser de PostgreSQL y la recorre como AST. Admite
exactamente una sentencia y solo si es un `SELECT`. Rechaza, entre otros:

- cualquier nodo de DDL o DML (`CreateStmt`, `InsertStmt`, `UpdateStmt`, `DeleteStmt`, `DropStmt`,
  `TruncateStmt`, `AlterTableStmt`, …);
- sentencias de control (`TransactionStmt`, `SetStmt`, `DoStmt`, `CopyStmt`, `PrepareStmt`,
  `ExecuteStmt`, `GrantStmt`, `RevokeStmt`, …);
- cláusulas de bloqueo (`FOR UPDATE`, `FOR SHARE`, y variantes);
- `SELECT INTO`;
- más de una sentencia.

El guard recolecta las relaciones referenciadas y las funciones invocadas; el ejecutor valida que
las relaciones estén dentro del alcance y que los parámetros sean contiguos. El guard es defensa
en profundidad: no sustituye un rol PostgreSQL genuinamente de solo lectura.

# 6. Fuente de datos y alcance

- El MCP usa una única conexión de solo lectura a SER v3.0 (`mode: readonly`).
- La configuración declara opcionalmente los objetos autorizados (`allow.schemas`,
  `allow.views`, `allow.materialized_views`); una referencia a un objeto no declarado produce
  `OBJECT_NOT_ALLOWED`.
- Cuando la lista está vacía, el alcance efectivo son los grants del rol PostgreSQL.
- Las exclusiones de campos y las reglas de clasificación viven en la capa de datos (vistas y
  permisos), no en el prompt.
- Los datos devueltos son contenido no confiable y nunca se interpretan como instrucciones.

# 7. Parámetros tipados

Cada parámetro tiene un tipo declarado y un valor. Los valores se envían como parámetros
posicionales (`$1`, `$2`, …) y nunca se interpolan en el texto SQL.

| Tipo | Forma |
|---|---|
| `int` | entero o cadena decimal canónica |
| `text` | string UTF-8 |
| `uuid` | string UUID canónico |
| `date` | `YYYY-MM-DD` |
| `bool` | boolean |
| `null` | valor nulo explícito |

Un conteo de parámetros inconsistente con la sentencia produce `PARAMETER_MISMATCH`; un tipo
incorrecto o un valor fuera de límite produce `INVALID_REQUEST`.

# 8. Límites y paginación

## 8.1 Valores iniciales

| Recurso | Default | Techo interno |
|---|---:|---:|
| frame MCP | 4 MiB | 8 MiB |
| nesting JSON | 32 | 64 |
| timeout total | 15 s | 60 s |
| filas por respuesta | 200 | 1,000 |
| bytes por valor | 256 KiB | 1 MiB |
| payload MCP estructurado | 2 MiB | 8 MiB |
| página de metadatos | 100 | 500 |
| requests activos | 4 | 32 |
| requests en cola | 4 | 64 |
| conexiones por pool | 4 | 32 |
| cola de audit | 64 | 1,024 |

`effective = min(techo interno, config del servidor, config de conexión, request override)`. Un
request que intenta elevar un límite produce `INVALID_REQUEST`; no se reduce en silencio. Cero
nunca significa ilimitado.

## 8.2 Paginación de metadatos

Keyset, nunca offset. El cursor identifica la posición de la página de metadatos; se codifica de
forma opaca y produce `INVALID_CURSOR` ante contexto inválido en lugar de recuperarse en
silencio.

# 9. Resultados y encoding

## 9.1 Forma

```json
{
  "columns": [
    { "name": "indicador", "postgres_type": "text", "type_oid": 25, "format": "text", "sensitive": false },
    { "name": "valor", "postgres_type": "numeric", "type_oid": 1700, "format": "text", "sensitive": false }
  ],
  "rows": [["Ingresos", "1234567890.123456789"]],
  "row_count": 1
}
```

Las filas conservan el orden de columnas y admiten nombres repetidos. Nunca se devuelven filas ni
valores parciales.

## 9.2 Representaciones

| Tipo | JSON |
|---|---|
| `null` | `null` |
| `bool` | boolean |
| `int2`, `int4` | number |
| `int8`, `numeric`, `decimal`, money | string |
| floats finitos | number |
| `NaN`, infinitos | string |
| text, enum, domain textual | string |
| UUID | string canónico |
| date | `YYYY-MM-DD` |
| timestamp | ISO 8601 sin zona, microsegundos preservados |
| timestamptz | RFC 3339 UTC, microsegundos preservados |
| bytea | base64 estándar |
| JSON/JSONB | texto JSON |
| arrays | texto PostgreSQL |

## 9.3 Truncación y atomicidad

- Cada valor se codifica por completo en un buffer acotado.
- Si un valor supera `value_bytes`, la operación falla con `RESULT_VALUE_TOO_LARGE`; no devuelve
  filas previas.
- Una fila se agrega solo si cabe completa dentro de filas y payload efectivos.
- Si la siguiente fila completa excede el límite, se detiene y se devuelve `truncated: true`.
- Errores de base, decode, timeout o cancelación descartan cualquier resultado parcial.

# 10. Configuración

El proceso recibe exactamente una ruta mediante `--config <path>`; se carga una vez y permanece
inmutable. La expansión `${NAME}` solo aplica a valores escalares; una variable ausente o vacía
es error.

El startup falla por versión de config inválida, claves desconocidas o duplicadas, nombres de
conexión inválidos, engine distinto de `postgres`, mode distinto de `readonly`, secretos vacíos
o sin expandir, DSN/URL o campo libre equivalente, objetos duplicados en la lista de permisos,
rango que exceda un techo, TLS inválido o destinos de logging y auditoría iguales.

# 11. Concurrencia, deadlines y cancelación

Todas las consultas consumen una unidad global de admisión. Hasta `active_requests` ejecutan;
hasta `queued_requests` esperan en FIFO; si la cola se llena se audita `SERVER_BUSY` y se
rechaza. El slot se retiene hasta confirmar la auditoría final.

Se deriva un único contexto con el deadline más temprano entre cancelación MCP, timeout global,
timeout de conexión, reducción del request y shutdown. La cancelación libera recursos y deja la
conexión en estado seguro; un resultado inválido, timeout o cancelación no finaliza el servidor.

# 12. Auditoría, logs y métricas

## 12.1 Auditoría

Sink JSONL local en append con permisos `0600`; rotación por tamaño con máximo de archivos. Cada
request genera dos eventos (attempt y outcome) con `request_id`, herramienta, conexión lógica,
outcome, `error_code` y `duration_ms`.

No se auditan SQL, parámetros, valores, filtros, resultados, DSN, host, port, user, database ni
password. Si el sink no confirma la entrega, el proceso pasa a `audit_failed`, detiene la nueva
admisión, cancela lo no ejecutado, intenta un log sanitizado y termina con exit code distinto de
cero.

## 12.2 Logs y métricas

Logs JSON a `stderr`, best-effort, con campos permitidos (timestamp, level, component, event,
`request_id`, herramienta, conexión opaca, error code sanitizado, duración). Nunca se pasa un
error pgx completo a logging. `stdout` no se referencia desde logging ni panic handlers.

Métricas internas bounded en memoria: requests por herramienta y outcome, active/queued, pool
acquire, duración, filas y bytes, audit latency/failures, cancelaciones y timeouts. No existe
endpoint remoto.

# 13. Operación stdio

`stdout` exclusivamente MCP. Un wrapper limita cada frame newline-delimited al techo
`mcp_frame_bytes` y rechaza nesting excesivo antes de materializar structs. No se abre ningún
listener. SIGINT, SIGTERM, EOF de stdin, fallo fatal de auditoría o error fatal del transporte
inician una secuencia única: detener admisión, cancelar queued, esperar activos hasta
`shutdown_timeout`, cancelar restantes, drenar auditoría, cerrar pools y terminar.

# 14. Estrategia de pruebas

| Nivel | Objetivo |
|---|---|
| Unitarias | Guard de AST, binding de parámetros, límites, cursores, encoding, error mapping. |
| Fuzzing | YAML, cursores, parámetros, JSON y encoder: nunca panic, nunca aceptar campo desconocido, nunca output parcial. |
| Integración (PostgreSQL real) | Rol de solo lectura, lista de permisos, rechazo de escritura/DDL, límites, cancelación, concurrencia, auditoría. |
| E2E MCP | `stdio`: schemas, structured output sin duplicación, cancelación, saturación, errores estables, `stdout` MCP-only. |

Casos críticos obligatorios:

- ninguna sentencia de escritura o DDL es aceptada;
- una referencia fuera de la lista de permisos se rechaza;
- un parámetro faltante o de tipo incorrecto se rechaza;
- no hay respuestas exitosas si la consulta falló;
- la auditoría registra también rechazos y fallas.

Comandos de verificación:

```bash
make fast   # gofmt, vet, tests, race, build
make e2e    # E2E MCP por stdio
make it     # integración con PostgreSQL real
```

# 15. Trazabilidad

| Requisito PRD | Diseño |
|---|---|
| RF-01 | §§2, 13 |
| RF-02 | §§4, 8.1 |
| RF-03 | §§5, 8.1 |
| RF-04 | §6 |
| RF-05 | §6 |
| RF-06 | §7 |
| RF-07 | §§4.3, 8.2, 9 |
| RF-08 | §8 |
| RF-09 | §11 |
| RF-10 | §12.1 |
| RF-11 | §§4.4, 4.5 |
| RNF-01..05 | §§8-13, 14 |

# 16. Secuencia de implementación

1. Bootstrap Go y build.
2. Config estricta, redacción, audit writer y stdio skeleton.
3. Contrato de herramientas, errores, límites y admisión.
4. Guard de AST y validación de entrada/parámetros.
5. Ejecución de solo lectura y encoder.
6. Herramientas de introspección y paginación de metadatos.
7. `db_query`, `db_explain` y validación de alcance.
8. Shutdown, logs/métricas, E2E y matriz PostgreSQL.

Cada incremento debe conservar `stdout` exclusivamente MCP y agregar sus pruebas en el mismo
cambio.

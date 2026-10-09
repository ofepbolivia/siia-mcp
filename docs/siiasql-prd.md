# PRD — SIIASQL MCP (MCP de SIIA)

**Producto:** SIIASQL — MCP de SIIA
**Binario:** `siiasql`
**Estado:** Alineado con la plataforma SIIA
**Versión:** 2.0
**Fuente de contexto:** `../../docs/siia-brd.md`, `../../docs/siia-prd.md`, `../../docs/siia-sdd.md`, `../../docs/siia-data-scope.md`

---

# 1. Resumen ejecutivo

SIIASQL es el MCP de SIIA. Expone al orquestador de IA de la plataforma una superficie de
**consultas SQL de solo lectura** sobre SER v3.0, junto con herramientas de introspección para
descubrir el esquema.

No ejecuta escritura ni DDL. Cada sentencia pasa por un guard de AST que admite únicamente un
`SELECT` de solo lectura; cualquier otra construcción se rechaza fail-closed. El alcance de los
objetos alcanzables se acota con la lista de permisos de la conexión y con los permisos reales
del rol PostgreSQL.

PostgreSQL (SER v3.0) es la única fuente soportada.

# 2. Contexto en SIIA

SIIA es una plataforma de consulta y análisis de solo lectura para autoridades y analistas. La
plataforma adoptó **consulta libre**: el usuario formula lo que necesita y el orquestador lo
resuelve contra SER v3.0 dentro de un alcance autorizado. Para sostener esa libertad, el MCP
acepta SQL de solo lectura en lugar de un catálogo cerrado de capacidades.

Reglas de la plataforma que este producto materializa:

- Se conserva la prohibición de **escritura, DDL y herramientas administrativas** (`RN-01`).
- La consulta libre se limita con un **guard de solo lectura** y límites de recursos.
- La exclusión de datos sensibles se resuelve en la **capa de datos** (vistas y permisos), no en
  el prompt.
- La autorización se aplica en la **API** y se revalida en el **MCP**.

# 3. Consumidores y beneficiarios

| Actor | Relación con el MCP |
|---|---|
| Orquestador de IA (McpGateway) | Único consumidor directo. Descubre el esquema, construye el SQL y lo ejecuta por el MCP. |
| Administrador técnico | Configura conexión, permisos de objetos y límites. No obtiene acceso funcional por administrar. |
| Autoridades y analistas | Beneficiarios indirectos: reciben respuestas construidas con resultados del MCP dentro de su alcance. |
| Usuario final | Nunca accede al MCP ni a la base de datos. |

# 4. Principios e invariantes

Estos requisitos no se sacrifican por velocidad, comodidad ni alcance:

- seguridad y privacidad;
- bloqueo de escritura y DDL;
- consulta solo a datos alcanzables por el rol y por la lista de permisos;
- límites de recursos;
- protección de credenciales;
- resultados correctos y sin pérdida silenciosa de precisión;
- auditoría obligatoria fail-closed.

# 5. Objetivos de v1

1. Ejecutarse como servidor MCP local por `stdio`, lanzado por el orquestador, sobre una única
   conexión a SER v3.0.
2. Exponer una superficie de consulta SQL de solo lectura y parametrizada.
3. Exponer herramientas de descubrimiento de esquema para construir consultas.
4. Rechazar cualquier sentencia que no sea un `SELECT` único de solo lectura.
5. Acotar el alcance con la lista de permisos de la conexión y los grants del rol.
6. Devolver resultados estructurados, completos y truncables en límites completos.
7. Limitar tiempo, filas y tamaño de respuesta.
8. Soportar solicitudes concurrentes y propagar cancelaciones.
9. Generar auditoría obligatoria sin registrar datos sensibles.
10. Mantener credenciales y coordenadas fuera de toda salida.

# 6. Alcance

- PostgreSQL (SER v3.0) como única fuente, por red privada o intranet, transporte `stdio`.
- Modo `readonly` como único modo válido, con una única conexión configurada a SER v3.0.
- Superficie de consulta SQL de solo lectura parametrizada.
- Herramientas de introspección de esquema (esquemas, tablas, columnas, índices, relaciones,
  muestras y conteos).
- Límites, cancelación, errores controlados y auditoría obligatoria.
- Pruebas con PostgreSQL real.

Fuera de alcance: escritura, DDL, herramientas administrativas, modos operador/admin, otros
motores, exportaciones masivas desde el MCP y cualquier listener de red.

# 7. Superficie de herramientas

## 7.1 Consulta

| Herramienta | Propósito |
|---|---|
| `db_query` | Ejecuta un `SELECT` de solo lectura parametrizado. |
| `db_explain` | Devuelve el plan de ejecución permitido, sin `ANALYZE`. |

## 7.2 Descubrimiento de esquema

| Herramienta | Propósito |
|---|---|
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

El catálogo de herramientas y sus esquemas de entrada son estables y versionados; se serializan
a golden files para que el contrato de cable no derive de los tipos Go.

# 8. Contrato de consulta

`db_query` recibe:

| Campo | Regla |
|---|---|
| `connection` | Identificador lógico de la conexión configurada. |
| `sql` | Un único `SELECT` de solo lectura, parametrizado con `$1`, `$2`, … |
| `parameters` | Lista de parámetros tipados (`type`, `value`); nunca se interpola texto. |
| `limits` | Reducción opcional de timeout, filas y bytes de respuesta. |

Respuesta lógica (envoltura común):

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

Las filas son posicionales y conservan el orden de columnas. Nunca se devuelven filas ni valores
parciales: si la siguiente fila completa excede un límite, el resultado se marca `truncated`.

# 9. Requisitos funcionales

## RF-01 — Rol y transporte

El MCP corre como proceso local por `stdio`, lanzado por el orquestador. `stdout` se reserva
para el protocolo MCP. No abre listeners ni endpoints remotos.

## RF-02 — Superficie de consulta

El MCP expone `db_query` y `db_explain` para ejecutar SQL de solo lectura parametrizado, y un
conjunto de herramientas de descubrimiento de esquema. Toda herramienta valida su esquema de
entrada y rechaza campos desconocidos.

## RF-03 — Guard de solo lectura

Toda sentencia se parsea y valida antes de ejecutarse. Se admite exactamente un `SELECT`; se
rechazan DDL, DML, cláusulas de bloqueo, `SELECT INTO`, sentencias de transacción y cualquier
construcción fuera de una lectura. El rechazo es fail-closed.

## RF-04 — Alcance de objetos

Los objetos alcanzables se restringen con la lista de permisos de la conexión (`allow.schemas`,
`allow.views`, `allow.materialized_views`) y con los permisos reales del rol PostgreSQL. Una
referencia fuera del alcance se rechaza.

## RF-05 — Autorización

El orquestador no puede ampliar el alcance desde el modelo ni desde el cliente. La autorización
de usuario se resuelve en la API; el MCP ejecuta con un rol de solo lectura y su propia lista de
permisos. La ausencia de autorización en la API deniega antes de llegar al MCP.

## RF-06 — Parametrización

Los valores se envían como parámetros posicionales tipados; nunca se interpolan en el texto SQL.
El conteo y los tipos de parámetros se validan contra la sentencia.

## RF-07 — Resultado estructurado

El MCP devuelve columnas tipadas (nombre, tipo PostgreSQL, OID, formato, marca de sensible) y
filas posicionales completas, dentro de una envoltura versionada. Las listas de metadatos son
paginables por cursor opaco y determinista.

## RF-08 — Límites

El servidor impone límites configurables y techos internos de tiempo, filas, tamaño de valor,
tamaño de respuesta y concurrencia. Un request puede reducir límites, nunca ampliarlos.

## RF-09 — Cancelación y concurrencia

La cancelación del orquestador detiene la espera y el trabajo en curso, libera recursos y no
deja la conexión en estado inseguro. Varias solicitudes pueden ejecutarse concurrentemente sin
mezclar respuestas, auditoría ni estado.

## RF-10 — Auditoría

Cada intento y resultado de consulta es auditable. La auditoría no incluye SQL, parámetros,
valores, credenciales ni DSNs. Es fail-closed: si no puede confirmarse su entrega, el servidor
deja de admitir operaciones.

## RF-11 — Errores controlados

Los errores usan códigos estables y mensajes seguros, sin SQL, objetos fuera de alcance,
detalles del driver ni coordenadas de conexión.

# 10. Seguridad y límites de confianza

- Permisos reales de PostgreSQL como frontera base, con rol genuinamente de solo lectura.
- Guard de AST como defensa en profundidad; no sustituye los permisos del rol.
- Lista de permisos de objetos como acotamiento adicional.
- Autorización de usuario en la API; el MCP no confía en el modelo ni en el cliente.
- Credenciales fuera de respuestas, logs y auditoría.
- TLS verificado en red privada o intranet; `disable` solo para sockets Unix, loopback o IP
  privada literal.

# 11. Requisitos no funcionales

## RNF-01 — Rendimiento

La capa MCP añade la menor latencia práctica sin debilitar los invariantes.

## RNF-02 — Recursos acotados

El proceso no admite trabajo, resultados ni esperas ilimitadas; al alcanzar su capacidad devuelve
un error retryable sin crecimiento no acotado.

## RNF-03 — Precisión

La serialización no produce pérdida silenciosa en `null`, booleanos, enteros, `NUMERIC`,
`DECIMAL`, floats, texto, UUID, fechas, timestamps, binarios, JSON/JSONB y arrays. En particular,
`NUMERIC`/`DECIMAL` se serializan como cadenas.

## RNF-04 — Robustez

Un resultado inválido, timeout o cancelación no finaliza el servidor ni inutiliza otras
consultas.

## RNF-05 — Mantenibilidad

Arquitectura modular, contrato de herramientas estable y versionado, y pruebas automatizadas.

# 12. Criterios de aceptación de v1

1. Se ejecuta como binario MCP por `stdio` y reserva `stdout` para el protocolo.
2. Expone `db_query`/`db_explain` y las herramientas de introspección, cada una con esquema
   validado.
3. Rechaza toda sentencia que no sea un `SELECT` único de solo lectura.
4. Restringe los objetos alcanzables por la lista de permisos y los grants del rol.
5. Aplica y respeta límites de tiempo, filas y payload; trunca solo en límites completos.
6. Preserva sin pérdida los tipos de RNF-03.
7. Propaga cancelaciones y soporta concurrencia acotada.
8. Audita cada intento/resultado sin datos sensibles y falla cerrado.
9. No retorna ni registra credenciales, DSNs, SQL, parámetros ni valores.
10. Se verifica con PostgreSQL real: guard, límites, permisos, cancelación y aislamiento.

# 13. Riesgos de producto

| Riesgo | Tratamiento |
|---|---|
| Una consulta intenta escribir o alterar datos. | Guard de AST fail-closed + rol de solo lectura + permisos de objetos. |
| El modelo intenta alcanzar objetos fuera de alcance. | Lista de permisos de objetos y rechazo de referencias no autorizadas. |
| Un resultado grande agota memoria o contexto. | Techos internos, truncación en límites completos y paginación en metadatos. |
| El rol PostgreSQL no es realmente de solo lectura. | Verificación operativa del rol como requisito de despliegue. |
| Sink de auditoría bloqueado. | Fail-closed: la trazabilidad prevalece sobre la disponibilidad. |

# 14. Entrega

v1 entrega la superficie de consulta de solo lectura, las herramientas de introspección, el guard
de solo lectura, límites, cancelación, concurrencia acotada, auditoría fail-closed, resultados
estructurados y pruebas con PostgreSQL real.

# 15. Trazabilidad a SIIA

| Requisito | Origen en SIIA |
|---|---|
| Solo lectura, sin escritura ni DDL | BR-03, RN-01, `siia-brd.md` |
| Consulta libre guardada | PRD §6, `DP-07` (consulta libre controlada) |
| Exclusión de datos sensibles en capa de datos | RN-05, `siia-data-scope.md` §11 |
| Autorización API + MCP | RN-03, NFR-01, `DA-05` |
| Contrato estructurado | FR-13, FR-14, SDD §10.4 |
| Auditoría y correlación | BR-08, NFR-05, SDD §14.2 |
| Límites | BR-09, FR-16, RN-08 |

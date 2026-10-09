# Documentación de SIIASQL MCP

Este directorio contiene la documentación de producto e ingeniería de SIIASQL, el MCP de SIIA.
La documentación se mantiene en español y se alinea con los documentos institucionales de
`../../docs/`.

## Documentos

| Documento | Propósito |
|-----------|-----------|
| [siiasql-prd.md](siiasql-prd.md) | Requisitos de producto y alcance del MCP. |
| [siiasql-sdd.md](siiasql-sdd.md) | Diseño de software, arquitectura, contrato de consulta, seguridad y verificación. |
| [user-stories/README.md](user-stories/README.md) | Índice de historias de usuario y orden de lectura. |

## Documentos de SIIA de referencia

- [`../../docs/siia-prd.md`](../../docs/siia-prd.md) — consulta libre y reglas del portal.
- [`../../docs/siia-sdd.md`](../../docs/siia-sdd.md) — contrato del orquestador y el MCP.
- [`../../docs/siia-data-scope.md`](../../docs/siia-data-scope.md) — clasificación y exclusión de datos.
- [`../../docs/siia-decision-register.md`](../../docs/siia-decision-register.md) — `DP-07` (consulta libre controlada).

## Convención

- Los archivos usan el sufijo `.md` en español.
- Al cambiar contenido normativo, actualiza en el mismo cambio los documentos afectados y sus referencias cruzadas.
- El MCP expone consultas SQL de solo lectura y guardadas; la escritura y el DDL siempre se rechazan.

## Verificación

Los cambios de documentación no requieren tests Go salvo que cambien comandos, schemas, contratos o ejemplos ejecutables. Si cambian ejemplos de configuración, valídalos contra `internal/config/config.go` y los tests del parser.

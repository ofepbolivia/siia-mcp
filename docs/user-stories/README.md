# Historias de Usuario

Este directorio contiene las historias de usuario de SIIASQL, el MCP de SIIA.
Cada historia vive en un archivo separado, con numeración estable para que pueda referenciarse
desde tareas, pruebas, issues o PRs sin depender del orden visual del documento.

## Orden de Lectura

| Archivo | Historia | Fuente principal |
|---|---|---|
| `001-superficie-herramientas.md` | Superficie de herramientas estable y versionada | PRD RF-02, §7 |
| `002-autorizacion-alcance.md` | Autorización en capas y alcance de objetos | PRD RF-05 |
| `003-alcance-objetos.md` | Alcance por lista de permisos y grants del rol | PRD RF-04 |
| `004-guard-solo-lectura.md` | Guard de solo lectura | PRD RF-03, RF-11 |
| `005-parametros-tipados.md` | Parametrización tipada de valores | PRD RF-06 |
| `006-contrato-estructurado.md` | Resultado estructurado y paginación | PRD RF-07 |
| `007-limites-recursos.md` | Límites de tiempo, filas y tamaño | PRD RF-08 |
| `008-cancelacion-concurrencia.md` | Cancelación y concurrencia segura | PRD RF-09 |
| `009-auditoria-obligatoria.md` | Auditoría obligatoria sin datos sensibles | PRD RF-10 |
| `010-operacion-stdio.md` | Operación MCP por stdio | PRD RF-01 |

## Convención

Cada historia usa esta estructura:

- Historia
- Valor
- Criterios de aceptación
- Reglas y restricciones
- Evidencia esperada
- Trazabilidad

Las historias describen valor y comportamiento observable. Los detalles internos de
implementación permanecen en `docs/siiasql-sdd.md`.

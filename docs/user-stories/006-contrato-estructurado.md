# 006 - Contrato Estructurado

## Historia

Como orquestador de SIIA, quiero recibir resultados estructurados y completos, para componer
respuestas confiables y presentar tablas sin ambigüedad.

## Valor

Establece el contrato de salida común: una envoltura versionada con columnas tipadas y filas
posicionales completas, y paginación determinista para las listas de metadatos.

## Criterios de Aceptación

- La respuesta de consulta usa la envoltura `{ schema_version, data, meta }`, donde `data`
  contiene `columns`, `rows` y `row_count`.
- Cada columna declara nombre, tipo PostgreSQL, OID, formato y marca de sensible.
- Las filas conservan el orden de columnas y admiten nombres repetidos.
- Nunca se devuelven filas ni valores parciales.
- `meta` indica duración y truncación (`truncated`, `truncation_reason`).
- Las listas de metadatos paginan con cursor opaco, no offset.
- Los datos completos aparecen una sola vez; el texto es un resumen breve.

## Reglas y Restricciones

- El cursor es opaco, no contiene credenciales y produce `INVALID_CURSOR` ante contexto inválido.
- Los valores sensibles a precisión se representan sin pérdida.

## Evidencia Esperada

- Tests de contrato de entrada y salida por herramienta.
- Tests de orden de columnas y nombres repetidos.
- Tests de truncación y paginación.

## Trazabilidad

- PRD: RF-07
- SDD: §§4.3, 8.2, 9
- SIIA: FR-13, FR-14, SDD §10.4

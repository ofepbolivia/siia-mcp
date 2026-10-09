# 008 - Cancelación y Concurrencia

## Historia

Como orquestador de SIIA, quiero que las cancelaciones detengan el trabajo en curso y que
solicitudes concurrentes no mezclen estado, para operar de forma confiable bajo carga.

## Valor

Evita que una consulta lenta o cancelada degrade el servidor, contamine la conexión o afecte
solicitudes posteriores o de otro usuario.

## Criterios de Aceptación

- La cancelación detiene la espera en cola, la adquisición de pool, la ejecución, el encoding o la espera de cache.
- Una cancelación libera recursos y deja la conexión en estado seguro.
- Varias solicitudes se ejecutan concurrentemente dentro de límites configurados.
- Las respuestas, auditoría y estado no se mezclan entre consultas ni requests.
- Un resultado inválido, timeout o cancelación no finaliza el servidor ni inutiliza la conexión.

## Reglas y Restricciones

- El contexto MCP se propaga hasta el driver PostgreSQL.
- La conexión posee un único pool reutilizado; no se crea conexión por request.

## Evidencia Esperada

- Tests de cancelación durante cola, pool acquire, ejecución e iteración.
- Tests de reutilización de pool tras cancelación.
- Tests concurrentes entre solicitudes y entre usuarios.
- Tests de robustez tras timeout y error.

## Trazabilidad

- PRD: RF-09, RNF-04
- SDD: §11

# 007 - Límites de Recursos

## Historia

Como responsable técnico, quiero límites configurables con techos internos para que ninguna
consulta consuma tiempo, memoria, conexión o contexto de forma no acotada.

## Valor

Protege el proceso MCP, PostgreSQL y el contexto del orquestador ante resultados grandes o
saturación, sin permitir que un request amplíe la política del proceso.

## Criterios de Aceptación

- El servidor aplica límites de tiempo, filas, bytes por valor, payload de respuesta, concurrencia y espera.
- Una solicitud puede reducir límites pero no ampliarlos sobre la política efectiva.
- Los límites configurados no pueden exceder los techos internos.
- Al alcanzar la capacidad configurada, el servidor devuelve un error retryable sin crecimiento no acotado.
- Los resultados se truncan solo en límites completos y reportan la truncación.

## Reglas y Restricciones

- Cero nunca significa ilimitado.
- `effective = min(techo interno, config del servidor, config de conexión, request override)`.
- Un valor que supera un techo produce `INPUT_LIMIT_EXCEEDED`.

## Evidencia Esperada

- Tests de bordes mínimos y máximos de configuración.
- Tests de request que intenta elevar límites.
- Tests de truncación por filas y payload.
- Tests de valor individual demasiado grande.
- Tests de saturación de admisión.

## Trazabilidad

- PRD: RF-08, RNF-02
- SDD: §8

# 005 - Parametrización Tipada

## Historia

Como analista, quiero que los valores de mis consultas viajen como parámetros tipados y no
interpolados, para obtener resultados acotados sin riesgo de inyección.

## Valor

Permite filtrar por cualquier dimensión presente en los datos sin exponer SQL interpolado: los
valores se enlazan como parámetros posicionales (`$1`, `$2`, …) validados por tipo y cantidad.

## Criterios de Aceptación

- Los valores se envían como parámetros tipados (`type`, `value`) y nunca se interpolan en el
  texto SQL.
- El conteo de parámetros se valida contra la sentencia; una inconsistencia produce
  `PARAMETER_MISMATCH`.
- Un tipo de parámetro incorrecto o un valor fuera del límite produce `INVALID_REQUEST`.
- Un valor `null` es válido solo de forma explícita.
- La parametrización no permite ampliar el alcance ni eludir el guard.

## Reglas y Restricciones

- Los parámetros son posicionales y contiguos.
- Los valores no se auditan; solo se registra la correlación por `request_id`.

## Evidencia Esperada

- Tests de tipos de parámetro.
- Tests de conteo inconsistente e intento de inyección.
- Tests de valor nulo explícito.

## Trazabilidad

- PRD: RF-06
- SDD: §7
- SIIA: `siia-data-scope.md` §10

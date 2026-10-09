# 001 - Superficie de Herramientas Estable

## Historia

Como administrador técnico, quiero que el MCP exponga una superficie de herramientas estable y
versionada —consulta e introspección—, para que el orquestador pueda construir consultas de solo
lectura sin depender de un contrato cambiante.

## Valor

Materializa una superficie explícita y acotada: herramientas de consulta (`db_query`,
`db_explain`) y de descubrimiento de esquema. Cada herramienta declara entrada cerrada y códigos
de error, y se serializa a golden files para que el contrato de cable no derive de los tipos Go.

## Criterios de Aceptación

- El MCP anuncia las herramientas de consulta y de introspección con sus esquemas de entrada.
- Los esquemas de entrada son cerrados: un campo desconocido produce `INVALID_REQUEST`.
- El orden y los esquemas de las herramientas no cambian sin actualizar los golden files.
- Cada herramienta declara sus códigos de error públicos.
- No existe ninguna herramienta de escritura, DDL ni administración.

## Reglas y Restricciones

- La superficie es estable y versionada; ampliarla exige actualizar el contrato y sus pruebas.
- La consulta libre se sostiene con herramientas genéricas de solo lectura, no con un catálogo
  de casos.

## Evidencia Esperada

- Tests de registro y de esquemas de entrada.
- Tests de campos desconocidos rechazados.
- Tests de golden files del contrato.

## Trazabilidad

- PRD: RF-02, §7
- SDD: §§3.3, 4.1
- SIIA: PRD §6, DP-07 (consulta libre controlada)

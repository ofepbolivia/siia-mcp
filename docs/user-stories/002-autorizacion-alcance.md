# 002 - Autorización en Capas

## Historia

Como responsable de seguridad, quiero que la autorización de usuario se resuelva en la API y que
el MCP solo ejecute con su rol de solo lectura y su lista de permisos, para que ni el modelo ni el
cliente puedan ampliar el acceso a los datos.

## Valor

Aplica autorización en capas: la API decide qué puede consultar cada usuario y el MCP ejecuta con
un rol de base de datos acotado. El alcance efectivo de objetos lo fijan la lista de permisos y
los grants del rol, no el prompt.

## Criterios de Aceptación

- La API autoriza al usuario antes de invocar el MCP; el MCP no confía en el modelo ni en el
  cliente para ampliar acceso.
- El MCP ejecuta con un rol PostgreSQL de solo lectura y su lista de permisos de objetos.
- Una referencia fuera del alcance produce `OBJECT_NOT_ALLOWED`.
- No existe un campo de entrada que permita al cliente elevar el alcance.
- Administrar la plataforma no concede acceso funcional.
- Una denegación se registra en auditoría.

## Reglas y Restricciones

- La autorización combina los controles de la API con el rol y la lista de permisos del MCP.
- El MCP no construye ni amplía alcance a partir de la entrada.

## Evidencia Esperada

- Tests de referencia fuera de alcance.
- Tests de rol de solo lectura y lista de permisos.
- Tests de ausencia de ampliación desde el cliente.

## Trazabilidad

- PRD: RF-05
- SDD: §6
- SIIA: SDD §8.3, DA-05

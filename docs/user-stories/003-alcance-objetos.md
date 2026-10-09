# 003 - Alcance de Objetos

## Historia

Como responsable de datos, quiero que el MCP limite los objetos alcanzables por una lista de
permisos y por los grants del rol PostgreSQL, para que una consulta no acceda a datos fuera del
alcance aprobado.

## Valor

Aplica el principio de menor privilegio en dos capas: la lista de permisos de la conexión
(`allow.schemas`, `allow.views`, `allow.materialized_views`) y los permisos reales del rol de
solo lectura. La exclusión de campos sensibles vive en la capa de datos, no en el prompt.

## Criterios de Aceptación

- Una referencia a un objeto fuera de la lista de permisos produce `OBJECT_NOT_ALLOWED`.
- Cuando la lista está vacía, el alcance efectivo son los grants del rol PostgreSQL.
- Las vistas y materialized views declaradas son las únicas alcanzables cuando la lista las
  restringe.
- Un objeto inexistente produce `OBJECT_NOT_FOUND`.
- Los permisos y RLS de PostgreSQL siguen siendo la frontera efectiva.

## Reglas y Restricciones

- La configuración declara los objetos autorizados; una referencia no declarada se rechaza.
- La lista no amplía los permisos del rol; solo los reduce.

## Evidencia Esperada

- Tests con lista de permisos y referencia fuera de ella.
- Tests con lista vacía y grants del rol.
- Tests de objeto inexistente.
- Tests de RLS y permisos de solo lectura.

## Trazabilidad

- PRD: RF-04
- SDD: §6
- SIIA: RN-05, `siia-data-scope.md` §11

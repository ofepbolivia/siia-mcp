# 004 - Guard de Solo Lectura

## Historia

Como responsable de seguridad, quiero que el MCP rechace toda sentencia que no sea un `SELECT`
único de solo lectura, para que la consulta libre no abra una ruta de escritura ni de DDL hacia
SER v3.0.

## Valor

Hace verificable la prohibición de escritura y DDL: cada sentencia se parsea como AST y se
rechaza fail-closed si contiene DML, DDL, bloqueos, `SELECT INTO` o más de una sentencia.

## Criterios de Aceptación

- Una sentencia que no sea un `SELECT` único produce `QUERY_REJECTED`.
- Se rechazan DDL/DML, `TRUNCATE`, `GRANT`/`REVOKE`, transacciones, `COPY`, `SET`, `DO`, cláusulas
  de bloqueo y `SELECT INTO`.
- Un error de parseo produce `QUERY_REJECTED`.
- Los errores no revelan SQL, objetos fuera de alcance ni detalles internos.
- El guard es defensa en profundidad; no sustituye un rol PostgreSQL de solo lectura.

## Reglas y Restricciones

- La validación es default-deny: solo se admite lo explícitamente permitido.
- Los datos devueltos por la base se tratan como contenido no confiable.

## Evidencia Esperada

- Corpus de sentencias de escritura/DDL, todas rechazadas.
- Tests de sentencias múltiples y de `SELECT INTO`.
- Tests de mensajes de error sin fugas.

## Trazabilidad

- PRD: RF-03, RF-11
- SDD: §§5, 4.4, 4.5
- SIIA: RN-01, `siia-brd.md`

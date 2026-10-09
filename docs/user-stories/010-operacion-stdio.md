# 010 - Operación MCP por Stdio

## Historia

Como administrador técnico, quiero ejecutar SIIASQL como proceso local por `stdio`, lanzado por
el orquestador, para integrar el MCP sin abrir listeners ni endpoints remotos.

## Valor

Mantiene el proceso local como frontera de confianza, reduce la superficie de red y aplica
`DA-03`: el navegador y el usuario final nunca alcanzan el MCP.

## Criterios de Aceptación

- El producto se ejecuta como binario MCP por `stdio`.
- `stdout` queda reservado exclusivamente para mensajes MCP.
- Los logs operativos se escriben en `stderr`; la auditoría usa su sink dedicado.
- El servidor no abre listeners ni endpoints remotos.
- El único consumidor es el orquestador de SIIA.

## Reglas y Restricciones

- No hay transporte HTTP en v1.
- Cada proceso `stdio` se trata como un único principal de confianza.
- El usuario final nunca invoca el MCP ni la base de datos.

## Evidencia Esperada

- Test E2E `stdio`.
- Test de ausencia de listeners.
- Test de que `stdout` no contiene logs ni auditoría.
- Test con la revisión de protocolo MCP soportada por el SDK fijado.

## Trazabilidad

- PRD: RF-01
- SDD: §§2, 13
- SIIA: DA-03, SDD §5.1

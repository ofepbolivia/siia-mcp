# 009 - Auditoría Obligatoria

## Historia

Como responsable de seguridad, quiero auditar cada intento de herramienta y su resultado sin
registrar datos sensibles, para tener trazabilidad institucional sin exponer parámetros,
valores, credenciales ni DSNs.

## Valor

Satisface `BR-08` y `NFR-05`: toda interacción relevante es reconstruible y la trazabilidad es
un invariante de release.

## Criterios de Aceptación

- Cada intento de herramienta genera un evento de auditoría.
- Cada resultado genera un evento asociado con el mismo `request_id`.
- La auditoría no incluye SQL, parámetros, valores, filtros, credenciales, hosts, usuarios ni DSNs.
- Si el sink no confirma la entrega, el servidor falla cerrado y deja de admitir operaciones.
- Un input malformado y una denegación también producen un outcome auditado.

## Reglas y Restricciones

- Auditoría y logs usan sinks físicamente separados.
- La correlación usa `request_id`; no se derivan fingerprints de consultas.
- La auditoría confirmada prevalece sobre la disponibilidad.

## Evidencia Esperada

- Tests de eventos attempt y outcome.
- Tests de redacción de datos sensibles.
- Tests de sink bloqueado o no disponible.
- Tests de input inválido y denegación auditados.

## Trazabilidad

- PRD: RF-10
- SDD: §12.1
- SIIA: BR-08, NFR-05, SDD §14.2

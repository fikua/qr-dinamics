# QR dinàmics

QR dinàmics per a petits negocis: crea un QR, imprimeix-lo i canvia'n el destí quan vulguis. El QR no deixa de funcionar mai, ni si el panell o la base de dades cauen.

Especificació: PRD «QR dinàmics» (claude.ai/artifact/XMyh9vxJtu8XaDwkabHNsC).

## Arquitectura

Dos binaris Go i un Postgres:

| Binari | Port | Què fa |
| --- | --- | --- |
| `cmd/panel` | 8080 | Panell del propietari (HTML renderitzat al servidor): registre, QR, estadístiques. Aplica les migracions. |
| `cmd/redirect` | 8081 | `GET /{codi}` → 302 al destí. Respon des d'una taula en memòria sincronitzada cada `SYNC_INTERVAL` i desada en una instantània local; arrenca i respon encara que la BD estigui caiguda. Registra els escanejos per lots, fora del camí de la petició. |

Organització per domini: `internal/accounts`, `internal/qrcodes`, `internal/redirect`, `internal/safety` i `internal/web`.

## Desenvolupament

```sh
make up                # Postgres + panell (http://localhost:8080) + redirecció (http://localhost:8081)
make test              # tests unitaris
make test-integration  # unitaris + integració contra el Postgres del compose
make lint
```

En dev, els correus (enllaç de verificació) s'escriuen al log: `docker compose logs panel`.

## Configuració

| Variable | Servei | Obligatòria | Notes |
| --- | --- | --- | --- |
| `APP_ENV` | tots dos | sí | `dev` o `prod` |
| `DATABASE_URL` | tots dos | sí | |
| `ABUSE_EMAIL` | tots dos | sí | enllaç «Denunciar» de la pàgina neutra (RF-11) |
| `PANEL_BASE_URL` | panell | sí | URL pública del panell (enllaços dels correus) |
| `REDIRECT_BASE_URL` | panell | sí | domini que codifica cada QR |
| `SAFE_BROWSING_API_KEY` | panell | en prod | sense clau, en dev no es comproven les URL |
| `SNAPSHOT_PATH` | redirecció | sí | el directori ha de ser escrivible |
| `SYNC_INTERVAL` | redirecció | no | per defecte `10s`; ha de ser < 1 min |
| `COUNTRY_HEADER` | redirecció | no | capçalera del proxy amb el país (p. ex. `CF-IPCountry`); la IP no es guarda mai |

## Estat respecte al PRD

Fet: US-01 a US-07, RF-01 a RF-06 i RF-09 a RF-11.

Pendent:
- US-08, US-09, RF-07 i RF-08: Stripe i els correus de facturació.
- US-10: exportació i esborrat RGPD.
- RF-12: vista d'administració per bloquejar QR o comptes.
- Regla de degradació en acabar la subscripció (depèn de Stripe).
- Correu SMTP per a prod: el panell es nega a arrencar amb `APP_ENV=prod` fins que existeixi.
- Interfície en castellà (ara només en català).
- Limitació d'intents de login.
- Decisions obertes del PRD que afecten el codi: la quota gratuïta (ara 3) i el que es mostra quan un QR està pausat (ara, la pàgina neutra).

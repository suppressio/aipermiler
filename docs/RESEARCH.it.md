🌐 [English](RESEARCH.md) | **Italiano**

# RESEARCH.md — Sessione di ricerca API (Giorno 1)

> v3 — corretto il caso d'uso primario: l'utilizzatore dell'app è **il singolo sviluppatore**, non un admin IT. Vuole monitorare il **proprio** credito periodico (mensile/settimanale) di Claude e/o Copilot — che sia un piano personale o un seat pagato dall'azienda — per capire se lo sta usando in modo efficiente prima che si esaurisca. Gli scenari "admin monitora il team" (Admin API Claude, Metrics API Copilot org/enterprise) sono stati esclusi: non rientrano nell'interesse del progetto.

---

## Risposta diretta

| Servizio | Il developer (non-admin) può monitorare da solo il proprio consumo? | Come |
|---|---|---|
| **Claude** | **Sì**, sia con piano personale sia con seat aziendale Team/Enterprise | Endpoint interno `claude.ai/api/organizations/{orgId}/usage` con il proprio cookie di sessione, oppure lettura locale se usa Claude Code |
| **GitHub Copilot** | **Sì con piano personale** · **Solo con workaround non ufficiale se il seat è assegnato dall'azienda** | Piano personale: endpoint ufficiale `premium_request/usage`. Seat aziendale: nessun endpoint pubblico self-service; unica fonte reale è l'endpoint interno non documentato `copilot_internal/user` (stesso usato dall'indicatore quota in VS Code) |
| **OpenAI / Codex** | **Sì**, per l'account ChatGPT (verificato con Plus) | Endpoint interno `chatgpt.com/backend-api/wham/usage` con l'access token della propria sessione web chatgpt.com (login integrato, Google compreso). Codex CLI non necessaria |

Dettagli e fonti sotto.

---

## 1. Claude — self-tracking del developer (CONFERMATO)

Un membro — **anche senza ruolo admin** — di un piano Team/Enterprise seat-based vede la propria barra di utilizzo andando su claude.ai in **Settings → Usage**: limite delle 5 ore, limite settimanale "tutti i modelli" e limite settimanale Opus, ciascuno con data di reset. Fonti ufficiali: [Team plan FAQ](https://support.claude.com/en/articles/9266767-what-is-the-team-plan), [Come funzionano i limiti di usage/length](https://support.claude.com/en/articles/11647753-how-do-usage-and-length-limits-work).

Dietro quella barra c'è un endpoint interno non documentato:

```
GET https://claude.ai/api/organizations/{organization_id}/usage
```

Raggiungibile con il **solo cookie di sessione (`sessionKey`) del developer stesso** — non serve alcun permesso admin, l'endpoint restituisce i dati personali di chi chiama. Risponde con `five_hour`, `seven_day`, `seven_day_sonnet`, `seven_day_opus`, ciascuno con `utilization` e data di reset. Confermato dal codice di due tool di terze parti che lo implementano: [linuxlewis/claude-usage (SPEC.md)](https://github.com/linuxlewis/claude-usage/blob/main/SPEC.md) e [steipete/CodexBar](https://github.com/steipete/CodexBar/blob/main/docs/claude.md).

**Claude Code (CLI) con seat aziendale:** i log locali in `~/.claude/projects/**/*.jsonl` vengono scritti normalmente indipendentemente dal tipo di licenza. Dalla versione 2.1.92 Claude Code espone anche i dati di rate-limit direttamente nella **statusline** ed esiste un comando **`/usage`** che mostra il consumo corrente — nessun bisogno di scraping. Fonti: [Claude Code — statusline docs](https://code.claude.com/docs/en/statusline), [tracking costi Claude Code](https://avinashsangle.com/blog/claude-code-cost-tracking), [cc-friend/ccost](https://github.com/cc-friend/ccost).

**SSO:** non risulta che cambi il meccanismo. Dopo un login SSO (SAML/OIDC via WorkOS, [guida ufficiale](https://support.claude.com/en/articles/13132885-set-up-single-sign-on-sso)) l'utente atterra comunque in una normale sessione browser su claude.ai, quindi il cookie `sessionKey` viene emesso allo stesso modo (TTL ~30 giorni). Ipotesi ragionevole ma non confermata al 100% da documentazione — alcune policy enterprise via MDM potrebbero imporre sessioni più corte; da verificare sul campo al primo test con un account reale.

**In pratica per l'app:** basta far fare all'utente un login "vero" in una finestra browser embedded (Electron `BrowserWindow`, sia con password/Google sia con SSO), catturare il cookie `sessionKey`, e interrogare l'endpoint interno periodicamente. In alternativa/complemento, se il developer usa Claude Code, si può leggere `/usage` o i JSONL locali senza rete.

**Addendum (Giorno 3 — verificato con un account reale): claude.ai è dietro Cloudflare.** Il solo cookie `sessionKey` non basta per superare il bot management di Cloudflare da una richiesta HTTP "nuda" (senza motore browser): senza anche il cookie `cf_clearance` (ottenuto dalla stessa sessione browser reale usata per il login) e uno User-Agent plausibile, l'endpoint risponde `403` con la pagina interstitial "Just a moment..." invece del JSON atteso. Fix implementato: `main/claude-auth.ts` ricostruisce l'header Cookie completo leggendo a runtime tutti i cookie della sessione Electron per il dominio claude.ai (non solo `sessionKey`), e `services/claude.ts` invia anche uno User-Agent da browser desktop. Limite noto non ancora risolto: se `cf_clearance` scade e Cloudflare richiede una nuova verifica JS interattiva, serve rifare il login (che riapre una vera `BrowserWindow` e quindi supera di nuovo la verifica) — non c'è ancora un meccanismo di refresh automatico in background.

**Addendum 2 (Giorno 3 — verificato con un account reale): i nomi dei campi della risposta usage non sono stabili.** Dopo aver superato Cloudflare, la risposta reale ricevuta da un account collegato NON conteneva più `five_hour`/`seven_day`/`seven_day_opus` valorizzati (tutti presenti ma `null`, insieme ad altri nomi storici come `seven_day_sonnet`/`seven_day_oauth_apps`/`seven_day_cowork`/`seven_day_omelette`, anch'essi `null`), bensì chiavi con nomi arbitrari non documentati — osservati in pratica: `cinder_cove`, `omelette_promotional`, `tangelo`, `iguana_necktie`, `nimbus_quill`. Esempio reale (valori reali della risposta, NON quelli mostrati in una eventuale segnalazione automatica — vedi sotto):

```json
{
  "five_hour": null, "seven_day": null, "seven_day_opus": null,
  "omelette_promotional": { "utilization": 0, "resets_at": null, "limit_dollars": null, "used_dollars": null, "remaining_dollars": null },
  "cinder_cove": { "utilization": 39.52, "resets_at": "2026-09-13T14:38:47Z", "limit_dollars": 1000, "used_dollars": 395.17, "remaining_dollars": 604.83 }
}
```

Ipotesi più probabile: offuscamento intenzionale lato Anthropic dei nomi dei campi (nomi-codice non semantici, es. nomi di frutta/luoghi), forse per scoraggiare lo scraping da parte di tool di terze parti come quelli citati sopra — non una semplice rinomina occasionale, dato che nessuno dei nomi storici documentati è sopravvissuto. Non è da escludere che i nomi ruotino ulteriormente in futuro.

**Fix implementato in `services/claude.ts` (`buildQuotaWindows`):** non si legge più per nome di campo fisso, ma per **forma del valore** — qualunque chiave il cui valore abbia un `utilization` numerico viene trattata come una finestra di quota valida, a prescindere dal nome. Se la finestra espone anche `limit_dollars`/`used_dollars` numerici (come `cinder_cove` sopra — verosimilmente un credito extra a consumo in dollari), viene modellata come `unit: 'count'` con gli importi reali invece che come semplice percentuale, sfruttando il modello multi-finestra già esistente (vedi ARCHITECTURE.md §0). Le finestre a 0% senza data di reset e senza importi (come `omelette_promotional` sopra) vengono scartate perché indistinguibili da un campo non applicabile al piano dell'account. Il vantaggio di questo approccio è che sopravvive a un'ulteriore rotazione dei nomi, finché la forma del JSON (un `utilization` numerico per finestra) resta la stessa — se anche quella dovesse cambiare, scatta comunque la diagnostica auto-segnalazione format-drift (vedi CLAUDE.md).

**Addendum 3 (2026-10-01 — account aziendale, issue #6): un nuovo oggetto `spend`.** Su un account aziendale (seat Team/Enterprise) tutte le finestre erano `null`, compreso `extra_usage.utilization`: nessuna finestra riconosciuta, quindi l'app ha segnalato un cambio di formato. Il dato si è spostato in due nuovi campi di primo livello: `spend` (`enabled`, `percent`, `severity`, `used`/`limit` come `{ amount_minor, currency, exponent }`, `cap.credits`, `disclaimer`, …) e `limits` (un array, vuoto su quell'account: la forma dei suoi elementi è ignota). **Correzione:** `buildQuotaWindows` legge `spend` come finestra "Limite di spesa": importi reali (`amount_minor / 10^exponent`) quando `used`/`limit` sono ben formati, altrimenti `percent`; `enabled: false` è uno stato riconosciuto senza finestra. Viene aggiunta solo se non esiste già una finestra in dollari, perché sugli account personali riporta lo stesso credito extra. `limits` non viene letto finché non si vede un esempio popolato; gli account personali in quel momento non erano toccati.

**Addendum 4 (2026-10-02 — stesso account aziendale): `extra_usage` è lo stesso budget di `spend`.** Dal pomeriggio del 2026-10-01 `extra_usage` è valorizzato: `utilization` (non arrotondata), `monthly_limit` e `used_credits` in unità minime con `decimal_places` (2 = centesimi), `currency`, `is_enabled`, `spend_limit_reached`, `user_disabled`, `disabled_reason`, `credits_ever_enabled`, più `daily` e `weekly` (finora null — forse futuri limiti giornalieri/settimanali, forma sconosciuta). Una segnalazione reale (valori non riportati qui) ha mostrato lo stesso importo usato e lo stesso limite di `spend.used`/`spend.limit`, con `spend.percent` arrotondato all'intero. **Fix:** `buildQuotaWindows` legge `monthly_limit`/`used_credits`/`decimal_places` come finestra mensile in dollari (`periodLength: 1`, con pacing, etichetta *Utilizzo extra (mensile)*); quando riporta gli stessi importi di `spend` resta solo `spend` (il suo storico è partito prima). Se un giorno divergessero, compaiono entrambe. La vecchia forma in dollari (`limit_dollars`/`used_dollars`, account personali, senza periodo) mantiene la regola precedente: `spend` non viene aggiunto. È stato osservato con la "segnalazione delle risposte di utilizzo" manuale (Impostazioni → Diagnostica).

**Addendum 5 (2026-10-02 — account personale, report di diagnosi): `limits` e `seven_day_breakdown`.** L'array `limits`, prima vuoto, è popolato: elementi `{ kind, percent, resets_at, is_active, severity, group, scope }`, con `kind: "session"` e `"weekly_all"` che riportano gli stessi valori di `five_hour` e `seven_day` (`is_active` indica il limite che sta vincolando ora). **Fix:** `buildQuotaWindows` legge `limits` solo come riserva quando nessuna finestra con nome è riconosciuta, associando i kind noti agli stessi id di finestra (lo storico continua); i kind sconosciuti hanno un id proprio e nessun pacing. Un nuovo oggetto `seven_day_breakdown` (`as_of`, `rows: [{ display_name, key, percent }]`) ripartisce il limite settimanale per modello — non ancora letto, candidato per una vista futura. Copilot `copilot_internal/user` su un seat aziendale: gli snapshot `chat` e `completions` hanno `unlimited: true` (nessuna quota da ritmare), `premium_interactions` ha un `entitlement` reale.

**Addendum 6 (2026-10-09 — account Claude Free, issue #44): il piano Free restituisce tutti i campi finestra come null.** Quando l'abbonamento scade e l'account torna al piano Free, l'endpoint `/usage` restituisce tutti i campi finestra offuscati come `null`, `limits: []` (vuoto) e `member_dashboard_available: false`. Lista dei campi osservati in quel momento: `amber_cistern`, `amber_gauge`, `amber_ladder`, `brass_thimble`, `cedar_ember`, `cinder_cove`, `copper_kite`, `extra_usage`, `five_hour`, `harbor_lantern`, `iguana_necktie`, `juniper_tide`, `nimbus_quill`, `omelette_promotional`, `seven_day`, `seven_day_breakdown`, `seven_day_cowork`, `seven_day_oauth_apps`, `seven_day_omelette`, `seven_day_opus`, `seven_day_sonnet`, `spend`, `tangelo`, `wattle_ember` — tutti `null`; più `limits: []`, `member_dashboard_available: false`, `weekly_scoped_shares: null`. **Fix:** `buildQuotaWindows` controlla `member_dashboard_available === false` prima di lanciare un `FormatDriftError`; quando è `false` non ci sono finestre di quota disponibili (il piano Free non ha barra di utilizzo nel prodotto) e viene restituito un array vuoto — stato noto, non un drift di formato.

---

## 2. GitHub Copilot — self-tracking del developer

### 2.1 Piano personale (Free/Individual/Pro+) — CONFERMATO, via ufficiale

Endpoint ufficiale e documentato, chiamabile dal developer con un proprio fine-grained PAT (permesso "Plan", read) o classic PAT:

- `GET /users/{username}/settings/billing/premium_request/usage`
- `GET /users/{username}/settings/billing/ai_credit/usage`

Fonte: [REST API — Billing usage](https://docs.github.com/en/rest/billing/usage). Pienamente self-service e automatizzabile.

**Addendum (2026-07-24 — switch a `ai_credit/usage`).** Implementato in `services/copilot.ts` lo switch da `premium_request/usage` (usato nella prima implementazione) a `ai_credit/usage`, la seconda alternativa già segnalata sopra ma non scelta all'epoca. Motivo: GitHub ha ritirato il modello "premium requests" il 1° giugno 2026 (vedi addendum in §2.2) — `premium_request/usage` non è formalmente deprecato (stessa forma di risposta, per doc REST), ma con ogni probabilità non riflette più consumo reale per gli account già migrati al nuovo sistema, quindi rischia di restituire sempre 0. Schema reale della risposta (diverso da quanto assunto nella prima implementazione, mai verificata con credenziali reali — vedi CLAUDE.md): un solo `timePeriod` per **tutto il report** (non una data per ogni `usageItem`: il filtro per mese avviene lato server coi parametri di query `year`/`month`), e ogni `usageItem` espone `netAmount` (importo netto in USD dopo eventuali sconti), non una `quantity` semplice. Conversione adottata in `sumCreditsUsed()`: somma di `netAmount` su tutti gli item del report, poi conversione USD → credit AI (1 credit = $0.01, vedi [GitHub Changelog, 19/06/2026](https://github.blog/changelog/2026-06-19-ai-credits-consumed-per-user-now-in-the-copilot-usage-metrics-api/)). **Non ancora verificato con un account reale** — da confermare con `npm test` + un vero PAT "Plan" (read-only) in locale.

**Addendum (2026-07-30 — primo test reale: `ai_credit/usage` risponde 404).** Con un account reale (lo stesso già usato per i test del §2.2, dove il consumo Copilot risulta gestito da un seat aziendale, non da un piano acquistato personalmente), `GET /users/{username}/settings/billing/ai_credit/usage` ha risposto `404 Not Found` (`{"message":"Not Found","documentation_url":".../billing-ai-credit-usage-report-for-a-user","status":"404"}`). Coerente con la doc REST, che specifica che gli endpoint utente "apply only if the user has purchased their own Copilot plan" — se l'accesso a Copilot arriva da un seat assegnato dall'organizzazione, l'endpoint personale può legittimamente non avere nulla da restituire per quello username, a prescindere da quale dei due path (`ai_credit`/`premium_request`) si chiami. Non è quindi chiaro se il 404 sia dovuto a questo (probabile) o a un rollout non ancora completo dell'endpoint. **Mitigazione implementata** in `services/copilot.ts` (`fetchBillingUsageReport`): un 404 su `ai_credit/usage` fa ripiegare automaticamente su `premium_request/usage` (stessa forma di risposta secondo la doc, nessuna differenza di parsing) prima di arrendersi — altri errori (rete, 401/403) non innescano il fallback, per non mascherare un problema di credenziali. Resta da verificare con un account che abbia davvero un piano Copilot **personale** (Free/Pro/Pro+) se anche in quel caso `ai_credit/usage` risponde 404 (endpoint non ancora disponibile) o 200 (confermando che il 404 osservato era dovuto solo al seat aziendale).

**Addendum (successivo — account personale Free: entrambi gli endpoint di billing rispondono 404).** Come documentato in `services/copilot.ts`: con un account personale Free reale sia `ai_credit/usage` sia `premium_request/usage` hanno risposto 404, nonostante la pagina github.com/settings/billing dello stesso account mostri un consumo reale di "Included credits" — questi endpoint REST ufficiali evidentemente non coprono quel tipo di piano. Il service ora fa un ultimo tentativo sull'endpoint interno `copilot_internal/user` (§2.2), che alimenta l'indicatore di quota di VS Code per qualunque account Copilot, trattato come best-effort.

### 2.2 Seat assegnato dall'azienda (Business/Enterprise) — NESSUN endpoint pubblico self-service (CONFERMATO)

Questo è il punto critico, verificato con fonti multiple:

- Gli stessi endpoint utente (`premium_request/usage`, `ai_credit/usage`) **restituiscono solo il consumo fatturato al piano personale** — se la licenza è gestita/fatturata dall'organizzazione, l'utilizzo del developer **non compare**. Fonti: [REST API — Billing usage](https://docs.github.com/en/rest/billing/usage), [Copilot user management](https://docs.github.com/en/rest/copilot/copilot-user-management).
- L'endpoint org (`/orgs/{org}/copilot/...`) con filtro `?user=` risponde **403 "cannot filter usage by user"** per org enterprise-owned.
- L'endpoint enterprise richiede un classic PAT con scope **`admin:enterprise`** — riservato ad admin.
- Conferme aggiuntive dalla community: [navikt/copilot issue #111](https://github.com/navikt/copilot/issues/111), [GitHub Community Discussion #184208](https://github.com/orgs/community/discussions/184208).

**Unica fonte reale del dato**, in questo scenario: l'indicatore di quota mostrato dall'estensione Copilot Chat in VS Code. Quell'indicatore legge da un endpoint **interno non documentato**:

```
GET https://api.github.com/copilot_internal/user
```

che restituisce `quota_snapshots` con `premium_interactions`, `chat`, `completions`, `quota_reset_date`, `copilot_plan` — esattamente i dati di cui ha bisogno l'app (consumo, quota, data di rinnovo), e funziona anche per seat assegnati da un'organizzazione perché è ciò che alimenta l'IDE stesso. Fonte: [GitHub Community Discussion #178117](https://github.com/orgs/community/discussions/178117). È tecnicamente replicabile con il token Copilot del developer, ma è **non documentato, senza versioning, e il suo uso fuori dai client ufficiali è in una zona grigia rispetto ai Termini di Servizio Copilot** — va trattato come workaround best-effort, non come integrazione stabile.

**Ipotesi confutata (2026-07-23) — il tipo di token non fa differenza.** Analizzando un vecchio prototipo dell'utente (`copilot_hypermiler`, mai eseguito con successo — nessun dato salvato nel checkout analizzato), era emerso che chiama lo stesso `copilot_internal/user` ma con un **token OAuth App di GitHub** (Authorization Code + PKCE, `github.com/login/oauth/authorize` + `/access_token`, scope `read:user`) invece di un Personal Access Token, con l'ipotesi che l'endpoint rispondesse con `quota_snapshots` completo solo a quel tipo di token. Implementato in `main/copilot-oauth.ts` e testato con un vero seat aziendale: **stessa identica risposta** ottenuta col PAT (nessun `quota_snapshots`, solo flag di feature/plan/org). Confermato non solo dal messaggio d'errore ma dalla firma di struttura (`shapeSignature()` in `services/_shape.ts`, calcolata su nomi di campo + tipo): la diagnostica auto-segnalazione ha correttamente **non riaperto** una seconda bozza di issue perché la firma coincideva esattamente con quella già segnalata per il PAT — prova che i due token ricevono una risposta strutturalmente identica. L'ipotesi è quindi chiusa: il tipo di credenziale (PAT classic/fine-grained, OAuth App) non influisce sulla risposta di `copilot_internal/user`; l'endpoint non espone più dati di quota per seat aziendale, punto e basta, a prescindere da come ci si autentica.

### 2.3 Progetti community verificati — confermano la stessa limitazione

- **[Fail-Safe/CopilotPremiumUsageMonitor](https://github.com/Fail-Safe/CopilotPremiumUsageMonitor)**: la modalità "Personal spend" usa l'endpoint Enhanced Billing personale con PAT "Plan" (read-only) → su un seat org-managed restituisce vuoto/404, stessa limitazione del §2.2. La modalità "Org" richiede `read:org` e dà solo metriche aggregate (utenti attivi, suggerimenti), non le premium request per singolo utente.
- Gli altri tracker noti (copilot-usage-tracker, m-marqx Copilot Premium Request Tracker) usano gli stessi endpoint di billing e condividono la stessa limitazione.

Nessun progetto community risolve in modo "pulito" il caso seat-aziendale; chi ci riesce (indicatori IDE) lo fa tramite l'endpoint interno del §2.2.

**Addendum (2026-07-24 — altre due piste esplorate su richiesta dell'utente, entrambe chiuse, nessuna sblocca il seat aziendale):**
- *Export Copilot Dashboard di Viva Insights* ([Microsoft Learn](https://learn.microsoft.com/en-us/viva/insights/org-team-insights/export-copilot-metrics)): richiede accesso "globale" al Copilot Dashboard (senior leader auto-rilevato, delega admin, Viva Insights Analyst o Global Admin M365) — la doc dice esplicitamente che "users with non-global access... can't access this feature": nessun percorso self-service per un developer non-admin. Anche disponendo di quell'accesso, i dati esportati sono metriche di *adozione* (prompt/azioni/giorni attivi, per-utente ma con ID anonimizzati), non consumo di AI Credits/quota residua — dominio di dati diverso da quello che ci serve.
- *Copilot Usage Metrics API — estensione all'attività della Copilot app* ([GitHub Changelog, 28/07/2026](https://github.blog/changelog/2026-07-28-github-copilot-app-usage-metrics-now-expand-across-report-rollups/)): stessa API già nota di §2.2/§3 (`ai_credits_used`), resta riservata a enterprise/organization owner o ruolo custom "View Copilot Metrics". La novità aggiunge solo l'attività della Copilot app (sessioni/richieste/prompt/token) ai rollup esistenti — non cambia il modello di permessi, non aggiunge un dato di quota residua per singolo utente non-admin.

**Addendum (ricerca 2026-07-23): perché `quota_snapshots` è sparito — GitHub ha cambiato modello di fatturazione, non solo formato.** Il 1° giugno 2026 GitHub è passato dalle "premium requests" (il modello su cui era costruito `quota_snapshots`) a un nuovo sistema **"AI Credits"** basato sul consumo di token ([GitHub Blog](https://github.blog/news-insights/company-news/github-copilot-is-moving-to-usage-based-billing/)). Non è quindi un drift qualsiasi: il modello di quota sottostante è stato ritirato a livello di prodotto. Il pannello "Credits" mostrato da VS Code (come nello screenshot dell'utente) è il nuovo "Copilot spend meter" introdotto in VS Code 1.125 (17-22 giugno 2026). **Punto decisivo, verificato**: anche Microsoft non espone questo dato via API — esiste una issue aperta e non risolta su `microsoft/vscode` che chiede esattamente "Expose credit usage... programmatically" ([microsoft/vscode#319571](https://github.com/microsoft/vscode/issues/319571)). L'unica API ufficiale con consumo AI Credits per singolo utente (`ai_credits_used`, aggiunto il 19/06/2026 alla Copilot Usage Metrics API) richiede permessi di **amministratore di organizzazione/enterprise** ([GitHub Changelog](https://github.blog/changelog/2026-06-19-ai-credits-consumed-per-user-now-in-the-copilot-usage-metrics-api/)) — esclusa dallo scope di questo progetto (vedi nota v3 in cima al file: "scenari 'admin monitora il team'... esclusi"). Anche tracker di terze parti aggiornati (es. `steipete/CodexBar`) hanno questo come lavoro esplicitamente aperto/non risolto. **Conclusione**: per un seat aziendale non esiste, ad oggi, nessuna via self-service nota — né nostra né altrui — per un developer non-admin. Il percorso *personale* (§2.1, `premium_request/usage`/`ai_credit/usage`) resta invece valido: la limitazione riguarda solo i seat gestiti da organizzazione/enterprise.

**Addendum (2026-07-23 — verificato con un account reale, seat aziendale): `copilot_internal/user` non restituisce più `quota_snapshots`.** Prima connessione reale di un account Copilot con seat aziendale (`accountScope: organization`): la risposta ricevuta non conteneva alcun campo di quota (né `quota_snapshots`, né `premium_interactions`/`chat`/`completions`, né una data di reset), solo flag di feature (`chat_enabled`, `cli_enabled`, `copilot_app_enabled`, ecc.), `copilot_plan`, `login`, `organization_list`/`organization_login_list` ed `endpoints` interni. Struttura completa (nessun valore reale) riportata dalla diagnostica auto-segnalazione — vedi CLAUDE.md, sezione "Diagnostica: auto-segnalazione format drift". Questo conferma sul campo il rischio già segnalato in §2.2 ("può rompersi senza preavviso"): l'endpoint non è più una fonte di quota utilizzabile, almeno per questo account/momento. Nessun fix tentato in `services/copilot.ts`: non c'è nulla da estrarre nella nuova risposta (zero campi riconducibili a percentuale/consumo), quindi non si può scrivere un parser senza inventare un formato non documentato (vietato da CLAUDE.md). Il comportamento dell'app è quello previsto: `FormatDriftError` → nessun crash, `emptyAccountSnapshot` con `lastError` mostrato nel widget invece di un dato inventato. Se in futuro GitHub esporrà un endpoint ufficiale per il consumo per-seat, va integrato lì; allo stato attuale monitorare un seat aziendale resta senza soluzione self-service affidabile (vedi anche §3).

**Addendum (2026-10-01 — gli snapshot del billing a token sono tornati, ma non per i seat gestiti dall'enterprise; issue #7).** Spunto: VS Code 1.132 mostra i "crediti AI usati" anche senza budget ([microsoft/vscode#319589](https://github.com/microsoft/vscode/issues/319589)). Il codice open source di VS Code (MIT) legge i crediti da **questo stesso endpoint** (`product.json` → `entitlementUrl: https://api.github.com/copilot_internal/user`, semplice `Authorization: Bearer`), con i campi di snapshot `credits_used`, `entitlement`, `quota_remaining`, `unlimited`, `has_quota`, `quota_reset_at` (epoch in secondi) e i campi radice `token_based_billing`, `quota_reset_date_utc` (`src/vs/base/common/defaultAccount.ts`, `chatEntitlementService.ts` → `getQuotaUsage`). Verificato con `scripts/probe-copilot-shape.js` (solo struttura):
- **account personale, token OAuth (GitHub CLI):** HTTP 200 con `quota_snapshots` completi (`chat`, `completions`, `premium_interactions`) compreso `credits_used`;
- **account aziendale, token OAuth:** HTTP 200 con `access_type_sku: "enterprise_managed"`, `assigned_date: null`, `organization_list`/`enterprise_list` vuote e **nessun** `quota_snapshots`/`token_based_billing`/`quota_reset_date` — come con il PAT a luglio. Nemmeno VS Code ha qualcosa da mostrare per un account così. L'unica fonte resta la Usage Metrics API riservata agli admin.
- **account aziendale, PAT, endpoint di billing personale:** `ai_credit/usage` ora risponde **400** `"Unable to get billing usage data."` invece di 404.

**Implementato in `services/copilot.ts`:** il 400 con quel messaggio è trattato come il 404 (si passa alla fonte successiva, gli altri 400 restano errori); gli snapshot sono letti con le regole di VS Code (illimitato → crediti usati senza totale; `entitlement > 0` → usato = entitlement − quota_remaining su entitlement; `entitlement: 0` o illimitato senza crediti → niente da mostrare, non un cambio di formato; altrimenti la percentuale); una risposta `enterprise_managed` senza snapshot lancia `CopilotUsageUnavailableError`, mostrato come spiegazione tradotta invece di una bozza di issue. Da verificare: se anche un PAT (e non solo un token OAuth) riceve gli snapshot su un account personale. **Verificato lo stesso giorno:** nemmeno la pagina "Copilot usage" delle impostazioni di github.com annunciata il 20/07/2026 mostra un consumo per questo account gestito dall'enterprise, e la Usage Metrics API riservata agli admin non è percorribile per l'utente. Conclusione: per un seat gestito dall'enterprise oggi **non esiste alcuna fonte**, né per l'app né per l'utente. Se GitHub inizierà a mandare `quota_snapshots` per questi seat, il parser attuale li leggerà senza modifiche al codice.

**Addendum (2026-10-01, più tardi lo stesso giorno — il seat aziendale vive su un tenant `.ghe.com`).** Il vicolo cieco qui sopra riguardava l'identità sbagliata: l'azienda dell'utente usa **GitHub Enterprise Cloud con residenza dei dati**, su un dominio proprio `<tenant>.ghe.com`. Il vero seat Copilot appartiene all'account di quel dominio (API su `api.<tenant>.ghe.com`); l'account github.com collegato all'enterprise non ha seat, da cui `enterprise_managed` senza dati. Osservato dall'utente nei DevTools di VS Code: `GET https://api.<tenant>.ghe.com/copilot_internal/user` con il token OAuth di VS Code risponde con `access_type_sku: "copilot_for_business_seat_quota"`, `copilot_plan: "business"`, `token_based_billing: true` e `quota_snapshots` completi — `chat`/`completions` illimitati con `credits_used`, `premium_interactions` con un `entitlement` in crediti, `quota_remaining`, `credits_used`; `quota_reset_at` vale 0, il reset arriva da `quota_reset_date`. VS Code ricava gli endpoint allo stesso modo (`githubEndpoints.ts`: `*.ghe.com` → `api.<host>`). **Implementato:** un "Dominio GitHub" per ogni account Copilot (`services/githubHost.ts`: solo `github.com` o `<tenant>.ghe.com`, perché il token viene inviato lì), usato per `/user`, billing, `copilot_internal/user` e OAuth (`<host>/login/oauth`); l'ambito aziendale non blocca più l'account come disattivato; le categorie illimitate a zero crediti non vengono mostrate. **Verificato lo stesso giorno con un PAT creato sul tenant:** stessa risposta (HTTP 200, `quota_snapshots` completi, `token_based_billing: true`) — basta un PAT, non serve una OAuth App. **Account personale Free, nell'app (stesso giorno):** collegandolo con OAuth compaiono le quote (`premium_interactions`, `chat`, `completions`) — il login OAuth non è più indicato come sperimentale. Valori reali del piano Free: `chat` 200 e `completions` 2000 con i propri `entitlement`/`quota_remaining`, `premium_interactions` con `entitlement: 0`, `has_quota: false`, `percent_remaining: 0` = **nessuna allocazione premium**, non "100% usato" (fino alla 0.4.2 compariva 100%; dalla 0.4.3 viene saltato, come in VS Code). Un PAT fine-grained con "Plan" riceve invece il report di fatturazione (spesa in dollari, al netto dell'allocazione inclusa). **Dalla v0.4.5 l'ambito personale prova prima `copilot_internal/user`** (quote vere) e usa il report di fatturazione solo come ripiego.

---

## 3. Implicazioni per l'app

Per l'MVP orientato al singolo developer, l'architettura più solida è:

1. **Claude:** login browser embedded (funziona sia per piano personale sia per seat aziendale, con o senza SSO) → cattura `sessionKey` → polling di `claude.ai/api/organizations/{orgId}/usage`. Se il developer usa anche/solo Claude Code, integrare la lettura di `/usage` o dei JSONL locali come fonte aggiuntiva (o alternativa, a rischio zero).
2. **Copilot:**
   - Se il developer ha un piano **personale**: PAT fine-grained ufficiale su `premium_request/usage` — via solida, headless, a basso rischio.
   - Se il developer ha un **seat aziendale**: nessuna via ufficiale. Opzioni da presentare come tali all'utente: (a) leggere manualmente l'indicatore di quota in VS Code (nessuna integrazione, solo istruzione a schermo), oppure (b) chiamare l'endpoint interno `copilot_internal/user` con il token Copilot del developer, etichettato chiaramente in UI come funzionalità **sperimentale/best-effort**, con avviso che può rompersi senza preavviso.

Per il modello di configurazione, resta valida la necessità di chiedere all'utente, per ciascun account collegato: metodo di login (classico o SSO — determina solo il flusso della finestra browser di cattura sessione, non la disponibilità del dato), se il piano è personale o assegnato dall'azienda (determina quale endpoint Copilot usare, e se serve avvisare l'utente che il dato Copilot sarà "best-effort"), tipologia di piano (per calcolare/validare la quota totale), e — dove l'API non fornisce reset date affidabile — la data di rinnovo inserita manualmente.

---

## 4. Raccomandazione architetturale

1. **Claude** (personale o aziendale, con o senza SSO): via primaria via endpoint interno + sessionKey, via secondaria/complementare Claude Code locale. Buona affidabilità, basso attrito per l'utente (login una tantum in finestra embedded).
2. **Copilot personale:** via ufficiale, priorità alta, implementare come primo servizio "solido".
3. **Copilot con seat aziendale:** funzionalità sperimentale via endpoint interno non documentato — implementare ma segnalare chiaramente il rischio in UI (può cambiare/rompersi senza preavviso, non è supportata da GitHub).

Fuori scope, per scelta esplicita: qualunque funzionalità di monitoraggio a livello di team/organizzazione (Admin API Claude, Metrics/Billing API Copilot org/enterprise) — l'app resta uno strumento per il singolo developer sul proprio consumo, non un pannello IT/admin.

---

## 5. Sorgenti dati locali per insight comportamentali (Addendum 2026-08-18)

Spunto: un pannello "Account & Usage" di VS Code (estensione Claude Code) mostra, oltre alla percentuale di quota, insight comportamentali — "87% del tuo utilizzo era a >150k di contesto", "22% da sessioni attive 8+ ore", breakdown per server MCP — con la didascalia esplicita *"Approximate, based on local sessions on this machine — does not include other devices or claude.ai"*. Confermato: **non è un dato dell'account recuperabile via l'endpoint che già usiamo** (`services/claude.ts`, `/organizations/{id}/usage` — solo `utilization`/`resets_at`/importi per finestra, nessun campo per-sessione: verificato per assenza di questi campi sia nel parsing sia in questo stesso file). È telemetria locale calcolata dall'estensione leggendo le proprie trascrizioni di sessione salvate su disco.

### 5.1 Claude Code (CLI + estensione VS Code) — CONFERMATO, stessa sorgente

- **Percorso**: `~/.claude/projects/<cwd-con-caratteri-non-alfanumerici-sostituiti-da-trattini>/<session-id>.jsonl`. Verificato empiricamente su questa macchina (`~/.claude/projects/-home-suppressio-Dev-electron-ia-hypermiler/*.jsonl`) — un file JSONL per sessione, una riga JSON per evento (messaggio utente, risposta assistant, chiamata di tool).
- **L'estensione VS Code usa lo stesso motore/formato della CLI**, non un percorso separato: confermato sia empiricamente (questa sessione di lavoro, condotta dentro VS Code, scrive esattamente in quel percorso) sia da fonti esterne. Per il developer, "Claude CLI" e "Claude Code in VS Code" sono quindi **un'unica sorgente**, non due voci separate in una eventuale lista di sorgenti configurabili.
- **Contenuto utile, ispezionato SOLO per struttura/chiavi (mai contenuto reale dei messaggi)**: ogni voce `assistant` ha `message.usage` con `input_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `output_tokens`, `output_tokens_details.thinking_tokens`, `server_tool_use` (conteggi `web_search_requests`/`web_fetch_requests`), oltre a `message.model`, `timestamp`, `sessionId`, `cwd`, `gitBranch`. La somma `cache_read_input_tokens + cache_creation_input_tokens + input_tokens` è la base diretta per un indicatore "dimensione contesto per turno" equivalente a quello di VS Code. I blocchi `content` di tipo `tool_use` hanno un campo `name` (nome dello strumento/server MCP invocato) leggibile senza guardare i parametri della chiamata.
- **Formato NON documentato e instabile fra versioni**: fonti multiple (doc ufficiale Claude Code, guide di terze parti) segnalano esplicitamente che il layout interno cambia release su release e raccomandano di non fare parsing diretto per script esterni, preferendo `/export` o le "interfacce a script" ufficiali. Il percorso stesso di storage è già cambiato una volta (febbraio 2026). Stessa cautela già applicata all'endpoint interno di `services/claude.ts`: parsing difensivo, mai un crash, mai un valore inventato se un campo manca.
- **Retention**: cancellazione automatica di default dopo 30 giorni (`cleanupPeriodDays`) — limita quanto indietro nel tempo può guardare qualunque indicatore costruito su questa fonte, indipendentemente da `history.retentionDays` (90gg) già usato per lo storico account-level.
- **Via consigliata — SDK ufficiale invece di parsing diretto**: esiste un package TypeScript ufficiale, `@anthropic-ai/claude-agent-sdk`, con `listSessions({ dir, limit })` e `getSessionMessages(sessionId, { dir, limit })` — astrae la scoperta dei file/il layout esatto su disco, più resiliente del parsing diretto del JSONL. *(Verificato in seguito con una chiamata reale: i messaggi restituiti includono `usage`; la dipendenza è stata approvata dall'utente ed è usata da `services/claudeLocalSessions.ts`.)*
- **Estensione JetBrains — sorgente DIVERSA, fuori scope ora**: legge da `~/.claude/sessions/<project-hash>/` (`session.json` + `conversation.jsonl`), non da `~/.claude/projects/`. Annotato per un'eventuale estensione futura, non affrontato ora: l'utente ha scelto esplicitamente di partire solo da questo IDE (VS Code).

### 5.2 Claude Desktop — NON CONFERMATO per l'uso "normale" con account claude.ai

- La documentazione ufficiale trovata (`claude.com/docs/third-party/claude-desktop/data-storage`) descrive esplicitamente la variante **"3P" (third-party/enterprise-managed)**: *"Claude Desktop on 3P has no Anthropic account. There is no sign-in step, no cloud-stored conversation history"* — un deployment diverso da quello con account claude.ai personale (pro/max/team/enterprise) che l'app già gestisce per Claude via `sessionKey`.
- In quella variante 3P, le sessioni Cowork/Chat/Code sono salvate localmente sotto `local-agent-mode-sessions/` (macOS: `~/Library/Application Support/Claude-3p/`, Windows: `%LOCALAPPDATA%\Claude-3p\`, spostato da `%APPDATA%` in un aggiornamento recente), con un `audit.jsonl` per sessione (invocazioni di tool, decisioni di permesso — concatenato via HMAC) ma **nessuna menzione esplicita di statistiche token/usage** in quel log.
- Per il Claude Desktop "normale" (account claude.ai standard, il caso rilevante per la maggior parte degli utenti dell'app): le conversazioni Chat vivono nell'account cloud, sincronizzate multi-dispositivo — è la ragione stessa di avere un account. Non risulta, dalla documentazione trovata, un log locale strutturato equivalente per questo caso.
- **Conclusione**: sorgente non confermata, a differenza di Claude Code (verificato empiricamente su questa macchina). Non includerla nella v1 della funzionalità; riverificare solo se in futuro serve davvero, con un'ispezione diretta su una macchina che ha Claude Desktop installato con un account claude.ai normale.

---

## 6. OpenAI / Codex — quota dell'abbonamento ChatGPT senza Codex CLI (Addendum 2026-10-18, #82)

**Esito: GO.** L'uso di un abbonamento ChatGPT/Codex si legge con lo stesso modello di Claude: login integrato in una partition Electron per account, poi chiamate HTTP dal main process. Non servono né la Codex CLI né un'estensione IDE. Verificato il 2026-10-18 con un account Plus reale (login Google); sono stati registrati solo stati e struttura delle risposte.

### 6.1 Da dove vengono i dati
Il metodo `account/rateLimits/read` dell'app-server della Codex CLI (il primo tentativo in #17) è solo un proxy: il binario della CLI chiama `GET https://chatgpt.com/backend-api/wham/usage` con il token OAuth ChatGPT del proprio login. Lo stesso endpoint risponde all'access token di una normale **sessione web** chatgpt.com.

Lo spike #142 (2026-10-27) ha eseguito davvero `account/rateLimits/read` e lo indica come fonte Codex per il core Go, senza cookie: vedi [SPIKE-CODEX-APP-SERVER.it.md](SPIKE-CODEX-APP-SERVER.it.md).

### 6.2 Flusso (verificato)
1. Finestra di login su `https://chatgpt.com/auth/login` in una partition dedicata, **User-Agent di Electron invariato**. Il login Google funziona. Riscrivere lo User-Agent (togliendo `Electron/x`) fa rifiutare il login a Google ("This browser or app may not be secure"): uno User-Agent in contraddizione con i client hints è proprio ciò che Google segnala. Non va mai toccato.
   Dopo il login chatgpt.com atterra sulla chat: la finestra va chiusa alla prima navigazione di ritorno su `chatgpt.com` in cui il controllo della sessione riesce (come fa `main/claude-auth.ts`), non a intervalli.
2. Login concluso quando `GET https://chatgpt.com/api/auth/session` restituisce un JSON con `accessToken` (un JWT). La sessione vive nei cookie `__Secure-next-auth.session-token.*` (circa 90 giorni); l'access token dura circa 10 giorni, quindi si rilegge da `/api/auth/session` a ogni refresh e non si salva mai.
3. `GET https://chatgpt.com/backend-api/wham/usage` con `Authorization: Bearer <accessToken>` → 200 JSON.

Risultati della sonda (una variabile alla volta):

| Richiesta | Risultato |
|---|---|
| Dalla pagina, Bearer + `ChatGPT-Account-Id` | 200 |
| Dalla pagina, solo Bearer | 200 |
| Dalla pagina, solo cookie, senza Bearer | 401 `{detail}` |
| Dal main process (`session.fetch` sulla partition, nessuna finestra aperta), `/api/auth/session` poi `wham/usage` | 200 e 200 |
| Dal main process con il `fetch` di Node (seconda prova, #86): `/api/auth/session` con l'header Cookie della partition + la sua User-Agent | 200, `accessToken` presente, nessun `cf-mitigated` |
| `fetch` di Node, `wham/usage` con Bearer (User-Agent della partition, con o senza cookie; anche la User-Agent di default di Node) | 200 |

Quindi i cookie da soli non bastano, il Bearer sì. `ChatGPT-Account-Id` non serve su un account personale; probabilmente serve a scegliere il workspace su Team/Enterprise (il JWT contiene `chatgpt_account_id` sotto `https://api.openai.com/auth`). I cookie Cloudflare (`cf_clearance`, `__cf_bm`) li imposta il login e `session.fetch` della partition li invia: nessuna challenge osservata.

**Scelta implementativa (#86):** funziona la stessa strada di Claude: `fetch` di Node tramite `services/_http.ts`, con l'header Cookie ricostruito dalla partition e la User-Agent della partition per `/api/auth/session`, poi il solo Bearer per `wham/usage`. `session.fetch` resta il fallback se Cloudflare iniziasse a rifiutare le richieste da Node.

Il token appartiene al client web di chatgpt.com (il suo `client_id`): nessun altro client viene impersonato. I token token-sharing di *Sign in with ChatGPT* sono rifiutati da questo endpoint (401 `rejected_by_access_enforcement`, openai/codex#54219), cosa che qui non conta.

### 6.3 Struttura della risposta (Plus, 2026-10-18)
Primo livello: `user_id`, `account_id`, `email`, `plan_type`, `rate_limit`, `code_review_rate_limit`, `additional_rate_limits`, `model_usage`, `credits`, `spend_control`, `rate_limit_reached_type`, `promo`, `rate_limit_reset_credits`.

- `rate_limit`: `allowed`, `limit_reached`, `primary_window`, `secondary_window`; ogni finestra ha `used_percent` (consumato, 0-100), `limit_window_seconds`, `reset_after_seconds`, `reset_at` (secondi Unix). Osservato: primaria 18000 s (5 h), secondaria 604800 s (7 giorni). Terze parti riportano una `primary_window` solo settimanale con `secondary_window: null`: la finestra si identifica da `limit_window_seconds`, mai dal nome del campo.
- `additional_rate_limits` e `code_review_rate_limit`: `null` qui; altri piani/modelli possono valorizzarli (forma non ancora osservata → gestione a prova di drift).
- `credits`: `has_credits`, `unlimited`, `overage_limit_reached`, `balance` (**stringa**), `approx_local_messages`, `approx_cloud_messages` (array di due numeri).
- `model_usage`: chiavi per nome del modello, `available`, `available_at`, `credits_would_enable`.
- `/api/auth/session` restituisce anche `user`, `account` (`planType`, `structure`, …), `sessionToken` e un campo `WARNING_BANNER`: niente di tutto ciò va mai nei log o al renderer.

### 6.4 Non ancora verificato
Free, Pro, Team/Enterprise (scelta del workspace, `ChatGPT-Account-Id`), SSO aziendale, `additional_rate_limits` valorizzato, comportamento alla scadenza del cookie di sessione (atteso: `/api/auth/session` senza `accessToken` → "accedi di nuovo").

### 6.5 Rischi
Endpoint interno non documentato, come quello di Claude: può cambiare senza preavviso → `FormatDriftError` e report di drift, come per gli altri provider. Leggere il proprio uso con la propria sessione è lo stesso schema di Claude; nessuna automazione della chat.

## Fonti

**Claude / Anthropic**
- [Team plan FAQ](https://support.claude.com/en/articles/9266767-what-is-the-team-plan)
- [Come funzionano i limiti di usage/length](https://support.claude.com/en/articles/11647753-how-do-usage-and-length-limits-work)
- [linuxlewis/claude-usage — SPEC.md](https://github.com/linuxlewis/claude-usage/blob/main/SPEC.md)
- [steipete/CodexBar — claude.md](https://github.com/steipete/CodexBar/blob/main/docs/claude.md)

**Sorgenti dati locali (Addendum 2026-08-18, §5)**
- [Claude Desktop — User identity and local data](https://claude.com/docs/third-party/claude-desktop/data-storage)
- [Claude Code — Manage sessions](https://code.claude.com/docs/en/sessions)
- [Claude Code — Work with sessions (Agent SDK)](https://code.claude.com/docs/en/agent-sdk/sessions)
- [Agent SDK reference — TypeScript](https://platform.claude.com/docs/en/agent-sdk/typescript)
- [Claude Agent SDK — Building a session browser (cookbook)](https://platform.claude.com/cookbook/claude-agent-sdk-05-building-a-session-browser)
- [anthropics/claude-agent-sdk-typescript — Issue #165, listing past sessions](https://github.com/anthropics/claude-agent-sdk-typescript/issues/165)
- [Claude Code — statusline docs](https://code.claude.com/docs/en/statusline)
- [Tracking dei costi in Claude Code](https://avinashsangle.com/blog/claude-code-cost-tracking)
- [cc-friend/ccost](https://github.com/cc-friend/ccost)
- [Set up SSO](https://support.claude.com/en/articles/13132885-set-up-single-sign-on-sso)
- Repo community aggiuntivi (piano personale, da v2): [lugia19/Claude-Usage-Extension](https://github.com/lugia19/Claude-Usage-Extension), [ryoppippi/ccusage](https://github.com/ryoppippi/ccusage), [phuryn/claude-usage](https://github.com/phuryn/claude-usage)

**GitHub Copilot**
- [REST API — Billing usage](https://docs.github.com/en/rest/billing/usage)
- [Copilot user management](https://docs.github.com/en/rest/copilot/copilot-user-management)
- [GitHub Community Discussion #184208](https://github.com/orgs/community/discussions/184208)
- [navikt/copilot issue #111](https://github.com/navikt/copilot/issues/111)
- [GitHub Community Discussion #178117 — copilot_internal/user](https://github.com/orgs/community/discussions/178117)
- [Fail-Safe/CopilotPremiumUsageMonitor](https://github.com/Fail-Safe/CopilotPremiumUsageMonitor)

**OpenAI / Codex (Addendum 2026-10-18, §6)**
- [openai/codex#54219 — token sharing cannot read plan usage](https://github.com/openai/codex/issues/54219)
- [steipete/CodexBar — codex.md](https://github.com/steipete/CodexBar/blob/main/docs/codex.md)
- [ajhochy/Rhythm#1568 — wham/usage response](https://github.com/ajhochy/Rhythm/issues/1568)
- [openai/codex#10869 — wham/usage polling](https://github.com/openai/codex/issues/10869)

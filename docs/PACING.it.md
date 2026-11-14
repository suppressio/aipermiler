🌐 [English](PACING.md) | **Italiano**

# PACING.md — Il modello di pacing

> Il modello dietro ogni numero che widget, grafico, notifiche e consigli mostrano: definizioni, casi limite, invarianti, stato e tabelle di esempi. È **indipendente dal codice**: quando codice e documento non concordano, il documento è l'intento e il codice è il bug (i disallineamenti noti sono al §9 e sono l'input del refactoring, #75). Le tabelle di esempi diventano i test a tabella della issue successiva.
>
> Stato: **bozza da rivedere** (#74). Con questo documento il codice non cambia.

---

## 0. Convenzioni

- **Punti percentuali di quota** (`pp`): ogni quantità di quota è una percentuale del totale della finestra, 0-100 (una finestra oltre il limite può superare 100). Una finestra `count` con totale viene prima normalizzata (`used / total × 100`); senza totale non ha utilizzo né pacing.
- **Unità**: `pp` (quota), `pp/unit` (per unità lavorativa), `pp/h` (per ora), `h` (ore), `unit` (unità lavorative, §2).
- **Arrotondamento**: un valore si arrotonda **solo per mostrarlo o salvarlo**, mai per usarlo in un altro calcolo (invariante I-4). Le tabelle mostrano il risultato arrotondato; le formule usano quello esatto.
- **Tempo**: tutti i giorni sono giorni di calendario **locali** (`YYYY-MM-DD` in ora locale), mai UTC.
- **`now`** è sempre un argomento: il modello non ha un orologio nascosto.
- **Scenario S**, usato da ogni tabella salvo diversa indicazione: orario lunedì-venerdì giorni interi (sabato e domenica liberi), `hoursPerDay = 8`, ciclo di fatturazione **[2026-10-01, 2026-11-01)**, `now` = **martedì 2026-10-20 10:00**, 3 ore di lavoro osservate oggi, nessun ritmo recente salvo indicazione. In S: il periodo ha **N = 22** unità lavorative (ottobre 2026 ha 22 giorni feriali), la quota equa è **s = 100 / 22 = 4,545 pp/unit** (0,568 pp/h su giorni da 8 h), 13 unità sono passate prima di oggi, oggi è un giorno intero (1 unità) di cui 0,375 trascorso (3 h / 8 h), quindi trascorse **E = 13,375**, rimanenti **R = 8,625**, e **U = 9** contando oggi per intero.

---

## 1. Tipi di finestra

Un provider espone una o più **finestre di quota** di un account. Il tipo decide quali quantità esistono.

| Tipo (`periodType`) | Esempio | Periodo | Reset | Quantità a giorni (§3.2-3.8) | Quantità a ore (§3.9-3.10) |
|---|---|---|---|---|---|
| **Ore mobili** (`rolling-hours`) | finestra da 5 ore di Claude/Codex | `periodLength` ore che terminano a `resetsAt` | `resetsAt` | **nessuna** (un «giorno» attraversa più reset) | tutte |
| **Giorni mobili** (`rolling-days`) | finestra settimanale di Claude/Codex | `periodLength` giorni che terminano a `resetsAt` | `resetsAt` | tutte | target, ritmo istantaneo |
| **Ciclo di fatturazione** (`billing-cycle`, durata nota) | quote mensili Copilot, `spend`/`extra_usage` Claude | `periodLength` mesi che terminano al reset | `resetsAt` della finestra, altrimenti `subscriptionRenewsAt` del provider, altrimenti il giorno del mese manuale | tutte | target, ritmo istantaneo |
| **Ciclo di durata ignota / crediti una tantum** (`periodLength = null`) | crediti riconosciuti solo dalla forma del valore | **inizio ignoto** | `resetsAt` proprio, se c'è | nessuna: solo i giorni al reset | solo ritmo istantaneo (nessun target, I-6) |

Una finestra senza data di reset e senza modo di ricavarla non ha alcuna quantità prospettica (§5, E6).

**Periodo.** `[start, end)` dove `end` è il reset e `start = end − periodLength` (ore, giorni o mesi di calendario). **Ore mobili e giorni mobili conservano i timestamp esatti**; un giorno mobile vale 24 ore. **I limiti dei cicli di fatturazione sono troncati al giorno locale** per contare le unità lavorative (§2), inclusa la parte di oggi già lavorata. Le finestre mobili richiedono il proprio `resetsAt`: rinnovo del provider e giorno manuale sono ripieghi solo per i cicli di fatturazione.

---

## 2. Tempo: calendario e lavoro

**D-2.1 Unità del giorno** `u(d)` ∈ {1, 0,5, 0} — giorno intero, mezzo, libero, dall'orario dell'account; **ogni giorno vale 1 con l'orario disattivato**. Dominio: un giorno di calendario locale.

**D-2.2 Unità lavorative tra** `W(a, b) = Σ u(d)` per i giorni locali `d` in `[a, b)`; `0` se `b ≤ a`. Per i cicli di fatturazione il totale del periodo è `N = W(start, end)`. Per i giorni mobili si usa `Wᵣ(a, b) = Σ u(d) × durata([a, b) ∩ d) / 24 h` sui giorni locali sovrapposti, con `0` se `b ≤ a`; quindi `N = Wᵣ(start, end)`. Con orario disattivato `u(d) = 1`; con orario attivo giorni interi, mezzi e liberi mantengono i propri pesi, anche nei primi e ultimi giorni parzialmente sovrapposti.

**D-2.3 Unità trascorse oggi** `e` **per i cicli di fatturazione** (la parte di oggi già lavorata), dominio `[0, u(oggi)]`:
- orario attivo: `0` finché oggi non si è vista attività; poi `min(u(oggi), max(2 h, lavorate) / hoursPerDay)` dove *lavorate* = ultimo aumento − prima attività del giorno (§3.1); `0` in un giorno libero;
- orario disattivato: frazione del giorno di calendario trascorsa (`10:00` → 0,4167).

Per i cicli di fatturazione D-2.4-D-2.6 valgono con `oggi` dentro il periodo, `start ≤ oggi < end` (giorni locali). Per i giorni mobili valgono con `start ≤ now < end` (istanti esatti). Fuori: **prima dell'inizio** `E = 0`, `R = U = N`; **dalla fine in poi** `E = N`, `R = U = 0`.

**D-2.4 Unità trascorse** `E = W(start, oggi) + e`.

**D-2.5 Unità rimanenti** `R = (u(oggi) − e) + W(oggi + 1, end)`. Invariante: `E + R = N` (I-8). Per i giorni mobili `E = Wᵣ(start, now)` e `R = Wᵣ(now, end)`: attività osservata e minimo di 2 ore non si applicano. Il loro `e = Wᵣ(max(start, oggi), now)` è la sovrapposizione pesata trascorsa oggi.

**D-2.6 Unità da oggi** `U = u(oggi) + W(oggi + 1, end)` — oggi contato per intero (`R` con `e = 0`). Si usa dove serve un numero fisso per il giorno (budget di oggi, quota ridistribuita). Per i giorni mobili `U = Wᵣ(max(start, oggi), end)`: conta solo la parte di oggi dentro la finestra e `U` rimane fisso durante quel giorno.

**D-2.7 Ore lavorative rimanenti** `H = R × hoursPerDay`, anche con l'orario disattivato (ogni giorno è allora un giorno lavorativo di `hoursPerDay` ore). Solo dove una finestra si legge in ore (§3.9) `H = ore di calendario al reset`.

| # | Input | Atteso |
|---|---|---|
| T-1 | lun-ven, [lun 10-05, lun 10-12) | W = 5 |
| T-2 | idem, venerdì `half` | W = 4,5 |
| T-3 | idem, orario disattivato | W = 7 |
| T-4 | b ≤ a | W = 0 |
| T-5 | S | N = 22, s = 4,545, E = 13,375, R = 8,625, U = 9 |
| T-6 | S, ancora nessuna attività | e = 0, E = 13, R = 9 |
| T-7 | S, attività ma solo 0,5 h lavorate | e = 2/8 = 0,25 (minimo di 2 h) |
| T-8 | S, 12 h di intervallo di attività | e = 1 (limitato all'unità del giorno) |
| T-9 | S di sabato 10-24 10:00 | e = 0, E = 17, R = 5, U = 5 |
| T-10 | S con orario disattivato (N = 31) | e = 10/24 = 0,4167, E = 19,4167, R = 11,5833, H = 92,67 h |
| T-11 | `now` prima dell'inizio del periodo | E = 0, R = N |
| T-12 | `now` dopo la fine del periodo | E = N, R = 0 |
| T-13 | S, venerdì 10-30 17:00, 7,9 h lavorate | e = 0,9875, R = 0,0125, H = 0,1 h |
| T-14 | S con `now` = lunedì 11-02 09:00, nessuna attività (ciclo finito, reset nuovo non ancora letto) | E = 22, R = 0, U = 0 |
| T-15 | HYP#78, 7 giorni mobili [mer 2026-10-14 23:00, mer 10-21 23:00), ora sab 10-17 18:38, orario disattivato | N = 7, E = 4058/1440 ≈ 2,82, R = 6022/1440 ≈ 4,18, U = 119/24, e = 1118/1440 |
| T-16 | stessa finestra, lun-ven interi, ora ven 10-16 12:00 | N = 5, E = 37/24, R = 83/24, U = 95/24, e = 0,5 |
| T-17 | stessa finestra, lun-ven interi, ora sab 10-17 18:38 (giorno libero dentro la finestra) | N = 5, E = 49/24, R = U = 71/24, e = 0 |
| T-18 | come T-16, venerdì mezzo | N = 4,5, E = 31/24, R = 77/24, U = 83/24, e = 0,25 |
| T-19 | 7 giorni mobili [mer 10-14 00:00, mer 10-21 00:00), ora sab 10-17 18:38, orario disattivato | N = 7, E = 5438/1440, R = 4642/1440, U = 4 |
| T-20 | 7 giorni mobili [mer 10-14 01:00, mer 10-21 01:00), ora sab 10-17 18:38, orario disattivato | N = 7, E = 5378/1440, R = 4702/1440, U = 97/24 |

---

## 3. Quantità

### 3.1 Usato, rimanente, attività di oggi

- **D-3.1 Usato** `x` (pp): l'utilizzo normalizzato della finestra adesso. Non limitato: 103 resta 103.
- **D-3.2 Rimanente** `rem = max(0, 100 − x)` (pp).
- **D-3.3 Intervallo di attività di oggi**: i campioni del giorno locale si confrontano con il precedente (il primo con la base del giorno `x₀`, §7); un aumento **sopra 0,05 pp** è attività, un calo è un reset, non attività. `start` = il campione prima del primo aumento (o il primo aumento stesso se è il primo campione del giorno); `end` = l'ultimo aumento; `start` anticipa alla prima sessione locale di Claude Code del giorno se è più presto. Nessun aumento → nessun intervallo. Ore lavorate = `end − start`.

| # | Input | Atteso |
|---|---|---|
| A-1 | base 57,00, campioni 09:00 57,00, 10:00 57,03 | nessun intervallo (aumento 0,03 ≤ 0,05) |
| A-2 | base 57,00, campioni 09:00 57,00, 10:00 58,00, 12:30 59,00 | start 09:00, end 12:30, lavorate 3,5 h |
| A-3 | base 57,00, primo campione del giorno 10:00 58,00 | start = end = 10:00, lavorate 0 h (vale il minimo di 2 h tramite D-2.3) |
| A-4 | base 57,00, campioni 09:00 57,00, 10:00 3,00 (reset) | nessun intervallo |
| A-5 | come A-2 e una sessione Claude Code iniziata alle 08:15 | start 08:15, lavorate 4,25 h |
| A-6 | normalizzazione: count 150/300; count senza totale; percentuale 37,5 | 50; nessuna; 37,5 |

### 3.2 Ritmo ideale

**D-3.4 Quota equa** `s = 100 / N` (pp/unit); la quota di un giorno è `s × u(giorno)`. Dominio: `N > 0`, altrimenti indefinita (niente pacing).
**D-3.5 Ritmo orario ideale** `s_h = s / hoursPerDay` per le finestre a giorni; `100 / periodLength` per le ore mobili (5 h → 20 pp/h).

### 3.3 Indice di efficienza

**D-3.6** `idx = s / (x / E)` — ritmo ideale sul ritmo medio effettivo del periodo; > 1 consuma meno del previsto, < 1 di più. Cumulativo dall'inizio del periodo, nessuna miscela. Nullo con `x < 0,1` (rumore), `N ≤ 0` o `E ≤ 0`. Non per le ore mobili.

### 3.4 Ritmo recente e ritmo misto

**D-3.7 Ritmo recente** `p_r` (pp/unit): sugli ultimi 3 giorni lavorativi **completati** dello storico (oggi escluso, giorni liberi e di reset saltati): `Σ delta / Σ u`. Nullo con meno di 2 giorni utilizzabili.
**D-3.8 Ritmo misto** `p = (x / E + p_r) / 2` (solo `x / E` se `p_r` è nullo; solo `p_r` se `E = 0`; nullo se nessuno dei due esiste). È l'unico ritmo usato da proiezione, autonomia e verdetto.

### 3.5 Proiezione al reset e autonomia

**D-3.9 Proiezione** `proj = x + p × R` (pp), **non limitata a 100** (227% dice di quanto il ritmo porta oltre). Senza ritmo → `x`.
**D-3.10 Autonomia** `a = (100 − x) / p` unità lavorative, `0` se `x ≥ 100`, `∞` se `p ≤ 0` o ignoto. Si confronta con il tempo rimasto `R`.

### 3.6 Ritmo istantaneo, tetto, target (il misuratore)

**D-3.11 Ritmo istantaneo** `v` (pp/h): dall'ultimo campione a o prima di `now − 1 h` (ma non più vecchio di `now − 2 h`; altrimenti il campione più vecchio dell'ultima ora) al più recente, `(x_new − x_old) / ore`. Nullo con meno di 2 campioni o meno di 5 minuti tra loro; un calo (reset) vale 0.
**D-3.12 Tetto** `C = rem / H` (pp/h) — il ritmo più veloce che non esaurisce la quota prima del reset, sulle ore effettivamente lavorabili (D-2.7; ore di calendario per le ore mobili e quando non resta tempo lavorativo). `0` se `rem = 0`.
**D-3.13 Target** `T = min(C, s_h)` — ciò con cui il misuratore confronta `v`. È il tetto quando l'utente è indietro (`C < s_h`: rallentare) e la quota oraria equa altrimenti: **il target non supera mai il tetto e non cresce mai perché il reset si avvicina** (I-1, I-2). Indefinito (il misuratore mostra `v` senza target) quando l'inizio del periodo è ignoto (`s_h` ignoto, I-6) o il reset manca o è già passato.
**D-3.14 Posizione sul misuratore** `g(v, T)`: `0` se `v ≤ 0`; `1` se `T ≤ 0`; altrimenti `0,5 + log₂(v / T) / 4` limitato a `[0,02, 1]` (target a metà, ½× a un quarto, 2× a tre quarti, ≥ 4× in fondo).

| # | Input | Atteso |
|---|---|---|
| G-1 | campioni 10:00 40,0, 10:30 41,0, 11:00 42,5; ora 11:00 | v = 2,5 pp/h (ancora 10:00) |
| G-2 | campioni 10:50 40,0, 11:00 40,2; ora 11:00 | v = 1,2 pp/h (nessuna ancora, il più vecchio nell'ora) |
| G-3 | campioni 10:56 40,0, 11:00 40,2 | v = nullo (< 5 min) |
| G-4 | campioni 08:30 40,0 (vecchio di 2,5 h), 10:50 41,0, 11:00 41,2 | ancora troppo vecchia: v da 10:50 → 1,2 pp/h |
| G-5 | campioni 10:00 40,0, 10:50 3,0 (reset), 11:00 3,5 | v = 0 (delta negativo a cavallo del reset limitato a 0) |
| G-6 | S, x = 60 | rem = 40, H = 69 h, C = 0,580, s_h = 0,568, **T = 0,57** |
| G-7 | S, x = 40 (avanti) | C = 0,870, T = s_h = 0,57 (mai sopra la quota equa) |
| G-8 | S, x = 80 (indietro) | C = 0,290, T = 0,29 |
| G-9 | finestra da 5 ore, 0 usato, 5 h al reset | C = 20, s_h = 20, T = 20 |
| G-10 | finestra da 5 ore, x = 67, 2 h al reset | C = 16,5, s_h = 20, T = 16,5 |
| G-11 | S, x = 100 | rem = 0, C = 0, T = 0; misuratore: 0 se v = 0, 1 se v > 0 |
| G-12 | S, x = 66,9, venerdì 10-30 17:00 (H = 0,1 h) | C = 331, **T = 0,57** (vedi §8, #72) |
| G-13 | g(5, 10) / g(10, 10) / g(20, 10) / g(40, 10) / g(0, 10) / g(0,1, 10) / g(5, 0) | 0,25 / 0,5 / 0,75 / 1 / 0 / 0,02 / 1 |

### 3.7 Budget di oggi

**D-3.15 Budget di oggi** `B = (100 − x₀) / U × u(oggi)` (pp) — ciò che restava all'**inizio** del giorno ripartito sulle unità lavorative da oggi in poi, per l'unità di oggi. **Fisso per tutto il giorno.** `x₀` è la base del giorno (§7). Indefinito (nullo) senza base, in un giorno libero, fuori dal periodo o con `U = 0`.
**D-3.16 Usato oggi** `used_today = x − x₀`, mai negativo; se il valore è sceso di **più di 1 pp** sotto la base nel giorno c'è stato un reset e `used_today = x` (il nuovo conteggio). Entrambi i lati alla **precisione salvata** (0,01 pp): confrontare un valore vivo con uno salvato richiede lo stesso arrotondamento. Un calo fino a 1 pp è rumore (un provider che ripiega su una percentuale intera), non un reset.
**D-3.17 Allarme di ritmo**: `used_today > 1,5 × B`, con `B > 0`.

| # | Input | Atteso |
|---|---|---|
| B-1 | S, x₀ = 57, x = 60 | B = 4,78, used_today = 3 |
| B-2 | S, x₀ = 57,04, x = 57,0449 | used_today = 0 (entrambi a 0,01: 57,04 − 57,04) |
| B-3 | S, x₀ = 40,0, x = 39,6 | used_today = 0 (calo 0,4 ≤ 1: non è un reset) |
| B-4 | S, x₀ = 40,0, x = 3,5 | used_today = 3,5 (reset) |
| B-5 | S, x₀ = 90, x = 103 | B = 1,11, used_today = 13, allarme (13 > 1,67) |
| B-6 | S di sabato | B = nullo |
| B-7 | S, nessuna base | B = nullo |
| B-8 | B = 4,78 | allarme sopra 7,17: usato 7,2 → sì; 7,17 → no |
| B-9 | S, venerdì 10-30 17:00, x₀ = 58, x = 60 | B = 42 (U = 1), used_today = 2 |

### 3.8 Quota ridistribuita

**D-3.18 Quota ridistribuita** `q = rem / U` (pp/unit): ciò che resta **adesso** sulle unità lavorative da oggi in poi (oggi per intero). Accanto la quota equa `s` e `U`. Segue il consumo di oggi mentre il giorno scorre (B no). Nulla con `U = 0` o `N = 0`. È il segnale di pacing primario: non richiede storico.
**D-3.19 Rapporto** `ρ = q / s`.

| # | Input | Atteso |
|---|---|---|
| Q-1 | S, x = 60 | q = 4,444, s = 4,545, U = 9, ρ = 0,978 |
| Q-2 | S, x = 40 | q = 6,667, ρ = 1,467 |
| Q-3 | S, x = 80 | q = 2,222, ρ = 0,489 |
| Q-4 | S di sabato 10-24, x = 70 | U = 5, q = 6,0, ρ = 1,32 |
| Q-5 | S, x = 100 | q = 0, ρ = 0 |
| Q-6 | inizio finestra, S il 10-01 09:00, x = 0 | U = 22, q = s = 4,545, ρ = 1 |

### 3.9 Prospettiva oraria (finestre a ore mobili)

**D-3.20** `hoursLeft = max(0, resetsAt − now)`; `projectedAtReset = x + v × hoursLeft` (nessun ritmo misurato vale 0); `autonomyHours = 0` se `x ≥ 100`, `(100 − x) / v` se `v > 0`, altrimenti nullo. Nulla senza istante di reset. **Reset passato** (`resetsAt ≤ now`, dati vecchi, E5): `hoursLeft = 0`, `projectedAtReset = x`, `autonomyHours` nullo qualunque siano `x` e `v`, e la finestra non ha verdetto prospettico (il passo 2 del §3.10 non si applica).

| # | Input | Atteso |
|---|---|---|
| H-1 | x = 67, 2 h al reset, v = 10 | proiettato 87, autonomia 3,3 h |
| H-2 | x = 67, 2 h al reset, v = 20 | proiettato 107, autonomia 1,65 h |
| H-3 | x = 67, 2 h al reset, v = nullo | proiettato 67, autonomia nulla |
| H-4 | x = 100, 2 h al reset | proiettato ≥ 100, autonomia 0 |
| H-5 | reset passato: resetsAt 09:00, ora 10:00, x = 80, v = 10 | hoursLeft 0, proiettato 80, autonomia nulla, verdetto nessun pacing |

### 3.10 Verdetto

Valutato in quest'ordine; vince il primo che si applica. `p` è il ritmo misto, `preliminary` è `E < 2`.

1. `x ≥ 100` → **esaurita**.
2. Ore mobili con il reset ancora davanti (`resetsAt > now`): `projectedAtReset > 100` → **a rischio** (con ore di autonomia), altrimenti **in linea**. Con il reset passato si scende al passo 5 (E5).
3. Con una ridistribuzione (`q`, `s > 0`):
   a. `p` noto, non preliminare, e `p > 1,5 × q` → **a rischio** (ritmo).
   b. `ρ < 0,5` → **a rischio**; `ρ < 0,95` → **indietro**; `ρ ≤ 1,05` → **in linea**; altrimenti **avanti**.
4. Nessuna ridistribuzione ma un ritmo: `a < R` → **a rischio** (autonomia); `proj > 100` → **a rischio** (proiezione); altrimenti **in linea**.
5. Niente di noto → **nessun pacing**.

Il widget si apre prima su una finestra esaurita o a rischio, poi sulla più usata, poi su quella con pacing.

| # | Input | Atteso |
|---|---|---|
| V-1 | S, x = 100 | esaurita |
| V-2 | S, x = 60 (ρ 0,978), p = 4,743 | in linea |
| V-3 | S, x = 65 (ρ 0,856), p = 4,86 | indietro |
| V-4 | S, x = 80 (ρ 0,489) | a rischio |
| V-5 | S, x = 40 (ρ 1,467) | avanti |
| V-6 | S, x = 60 (ρ 0,978), p = 7,0 (> 1,5 × 4,444 = 6,667) | a rischio (ritmo) |
| V-7 | come V-6 ma preliminare | in linea |
| V-8 | ρ esattamente 0,95 / 1,05 / 0,5 | in linea / in linea / indietro |
| V-9 | ore mobili, proiettato 87 / 107 | in linea / a rischio (1,65 h) |
| V-10 | nessuna ridistribuzione, a = 8,4 < R = 8,625 | a rischio (autonomia) |
| V-11 | periodo ignoto, nulla calcolabile | nessun pacing |
| V-12 | scelta: 5 ore al 67 % in linea, settimanale al 55 % a rischio | prima la settimanale |

### 3.11 Valutazione (costanza)

**D-3.21 Delta giornaliero** di un giorno completato: `x_fine − x₀` di quel giorno (pp), nullo se negativo (reset nel mezzo, non attribuibile); la base del giorno se registrata, altrimenti l'ultimo valore del giorno prima. **Quota ideale del giorno** `s × u(giorno)`.
**D-3.22 Valutazione**: sugli ultimi 7 o 30 giorni completati con quota ideale > 0 e un delta: per giorno `r = min(3, quota / delta)` (3 se `delta = 0`), `avg = media(r)`, stelle `5` se `avg ≥ 1,5`, `4` se `≥ 1,1`, `3` se `≥ 0,9`, `2` se `≥ 0,6`, altrimenti `1`. I giorni liberi sono esclusi. **Nessuna valutazione** (nulla) senza giorni utilizzabili, **senza alcun consumo** nei giorni valutati, o se la finestra è a ore mobili o senza pacing. Oggi non si valuta mai.

| # | Input | Atteso |
|---|---|---|
| R-1 | quota 4,55; delta 2,0, 4,55, 9,1 | rapporti 2,275, 1, 0,5; media 1,26 → **4 stelle** |
| R-2 | delta 0, 0 | nessuna valutazione (niente consumato) |
| R-3 | delta 0, 1,0 (quota 4,55) | rapporti 3, 3 (limitati) → media 3, 5 stelle |
| R-4 | un giorno di reset (delta nullo) tra due giorni normali | il giorno di reset è ignorato |
| R-5 | media 1,5 / 1,1 / 0,9 / 0,6 / 0,59 | 5 / 4 / 3 / 2 / 1 stelle |
| R-6 | un sabato con uso 3,0 | escluso (quota 0) |

(#67 sostituisce indice di efficienza e stelle con una nuova lettura della costanza; questa tabella descrive ciò che si mostra oggi ed è il posto dove mettere i casi della nuova definizione.)

### 3.12 Grafico, picco/media, serie

**D-3.23 Slot del grafico**: gli ultimi 7 o 30 giorni che terminano oggi e 2 o 5 giorni a venire. Per slot: `delta` (passato e oggi), `fullShare = s` (solo dentro il periodo), `dayUnit` e il **budget mobile**:
- giorno passato con una base `x₀(d)`: `(100 − x₀(d)) / W(d, end) × u(d)`;
- oggi: `B`;
- giorno futuro: `q × u(d)`;
- fuori dal periodo o senza pacing: nessuno.
`overBudget = delta > limite` dove `limite` è il budget mobile, o `s × u(d)` se non c'è; disuguaglianza stretta.
**D-3.24 Picco / media**: sui giorni completati (prima di oggi) con un delta, giorni liberi inclusi.
**D-3.25 Serie**: contando all'indietro dal giorno completato più recente; un giorno libero senza uso si salta; un giorno `overBudget`, o senza alcun limite, la interrompe. Nulla senza un limite con cui confrontare o senza alcun consumo.

| # | Input | Atteso |
|---|---|---|
| C-1 | S, gio 10-15, x₀ = 52 | budget = 48 / 12 × 1 = 4,0 |
| C-2 | delta 4,2 / 4,0 contro 4,0 | oltre / non oltre |
| C-3 | S, x = 60, slot mer 10-21 | q × 1 = 4,44 |
| C-4 | S, slot sab 10-24 | 0 |
| C-5 | slot prima del 10-01 | nessun budget, nessuna quota, mai oltre |
| C-6 | giorni completati (dal più recente): lun 2,0 sotto, dom 0, sab 0, ven 3,5 sotto, gio 5,0 oltre | picco 5,0, media 2,1, serie 2 |
| C-7 | tutti i delta 0 | picco 0, media 0, serie nulla |
| C-8 | un solo giorno completato | picco/media nascosti (servono 2 giorni) |

### 3.13 Riepilogo proiezione e «preliminare»

Proiezione e autonomia poggiano su `E` unità lavorative; sotto **2** sono segnate *preliminari* e il colore di avviso e la priorità del ritmo nel verdetto sono spenti.

| # | Input | Atteso |
|---|---|---|
| P-1 | S, x = 60, p_r = 5,0 | x/E = 4,486, p = 4,743, proj = **100,9**, a = **8,4**, giorni lavorativi al reset 8,6 |
| P-2 | S, x = 60, senza p_r | p = 4,486, proj = 98,7, a = 8,9 |
| P-3 | S il 10-01 09:00, x = 0 | E = 0, p nullo, proj = 0, a = ∞ (nascosta), preliminare |
| P-4 | S, x = 100 | a = 0 |
| P-5 | S, x = 0,05 | indice di efficienza nullo (< 0,1) |
| P-6 | S, x = 60 | indice di efficienza = 4,545 / 4,486 = **1,01** |
| P-7 | S, x = 103 | p = 7,70, proj = 169,4 (senza limite); vince il verdetto esaurita |

### 3.14 Consigli

Un consiglio è un **dato** (`{ key, params }`); i candidati sono quelli la cui condizione vale, se ne sceglie uno a caso tra loro, nessuno → nessun consiglio. **Una quota esaurita (`x ≥ 100`) non ha alcun candidato** (I-3, D3): le condizioni sotto valgono solo con `x < 100`. Un consiglio si mostra solo quando aggiunge un'azione o una causa che nessun altro numero dice.

| Chiave | Condizione | Parametri |
|---|---|---|
| `autonomy` | non preliminare, `x < 100`, `0 < a < R` (finita), e la riduzione mostrata è **tra 1 % e 99 %** | `a`, `R`, `reduction = (1 − a / R) × 100` |
| `rating` | stelle ≥ 4 | stelle, media |
| `nearReset` / `nearResetToday` | giorni al reset ≤ 2 (0 → *oggi*) e `x ≥ 70` | giorni, `x` |
| `cause` | la causa di consumo è chiara (§3.15) | percentuali, giorni |

| # | Input | Atteso |
|---|---|---|
| TP-1 | a = 8,4, R = 8,6 | `autonomy`, riduzione 2 % |
| TP-2 | a = 0 (x = 100) | nessun consiglio `autonomy` (§8, #73) |
| TP-3 | a = 0,04, R = 8,6 (riduzione 99,5 → 100) | nessun consiglio `autonomy` |
| TP-4 | a = 8,58, R = 8,6 (riduzione 0,23 → 0) | nessun consiglio `autonomy` |
| TP-5 | stelle 3 | nessun consiglio `rating` |
| TP-6 | x = 72, 2 giorni / 0 giorni al reset | `nearReset` / `nearResetToday` |
| TP-7 | x = 69, 1 giorno al reset | nessun consiglio |
| TP-8 | preliminare | nessun consiglio `autonomy` |
| TP-9 | x = 100, 1 giorno al reset, 5 stelle, causa chiara | nessun consiglio (esaurita) |

### 3.15 Insight locali (Claude, opzionali)

**D-3.26 Resa**: sui giorni presenti sia nello storico della quota sia nelle sessioni locali, con delta positivo e token di output: `Σ token di output / Σ delta` (token per pp). Servono ≥ 3 giorni e ≥ 1 pp in totale. Tendenza: resa della seconda metà sulla prima, in %.
**D-3.27 Causa del consumo**: ≥ 5 giorni confrontati; giorni sopra la mediana dei delta contro gli altri; dichiarata solo quando la quota di token prodotti sopra 150k di contesto è ≥ 1,5× e ≥ 20 punti più alta nei giorni pesanti.

| # | Input | Atteso |
|---|---|---|
| Y-1 | token/delta: 30k/3, 45k/3, 60k/3 | 15 000 token/pp; tendenza +100 % |
| Y-2 | solo 2 giorni | nulla |
| Y-3 | delta totale 0,8 pp | nulla |
| Y-4 | delta 1..5, quota di alto contesto 60 % (giorni 4-5) contro 20 % (giorni 1-3) | causa (60, 20, 5 giorni) |
| Y-5 | idem, 30 % contro 20 % | nulla (scarto 10 < 20) |
| Y-6 | 4 giorni | nulla |

### 3.16 Fine periodo e rinnovo

La fine del periodo è, in ordine: il `resetsAt` della finestra, il `subscriptionRenewsAt` del provider, il giorno del mese manuale (`resolveRenewalDate`: la prima occorrenza strettamente dopo `now`). È supportato solo `dayOfMonth`. Un giorno oltre la lunghezza del mese (29-31) vale l'ultimo giorno di quel mese, mai il mese seguente.

| # | Input | Atteso |
|---|---|---|
| N-1 | giorno 15, ora 10-20 | 2026-11-15 |
| N-2 | giorno 20, ora 10-20 10:00 | 2026-11-20 (il giorno è già iniziato, strettamente dopo) |
| N-3 | reset di finestra 10-27 e rinnovo del provider 11-01 | fine 10-27 |
| N-4 | nessun reset di finestra, rinnovo del provider 11-01 | fine 11-01 |
| N-5 | nessuno dei due, giorno manuale 15 | fine 2026-11-15 |
| N-6 | giorno 31, ora 10-20 | 2026-10-31 |
| N-7 | giorno 31, ora 10-31 | 2026-11-30 (mese più corto: il suo ultimo giorno) |
| N-8 | giorno 30, ora 01-31 | 2026-02-28 (l'occorrenza di gennaio è passata; febbraio è ridotto all'ultimo giorno) |
| N-9 | giorno 29, ora 2028-02-10 | 2028-02-29 (anno bisestile) |

### 3.17 Notifiche

| Notifica | Condizione | Frequenza |
|---|---|---|
| Soglia | una finestra dell'account ha `x ≥ soglia` (predefinita 80; 79,99 no) | una volta al giorno per account |
| Ritmo | una finestra con pacing ha `used_today > 1,5 × B`, `B > 0` | una volta al giorno per account |

### 3.18 Colori

Valore principale e proiezione: arancione 5 pp sotto la soglia, rosso dalla soglia (nessun colore se preliminare). Usato/budget di oggi: arancione da `soglia %` del budget, rosso oltre il 100 % di esso. Barra del grafico: rossa se `overBudget`. Tutto dalle stesse quantità di sopra, mai da copie arrotondate a parte.

---

## 4. Cadenza dell'aggiornamento (contesto per lo stato)

Un aggiornamento legge il provider, registra, calcola, tiene il risultato in memoria per il renderer. Dopo una lettura fresca in cui una finestra è salita, la successiva arriva dopo 5 minuti, altrimenti dopo l'intervallo configurato; i fallimenti rallentano per account. Solo i valori **appena letti** sono campioni. Vedi CLAUDE.md, *Auto-refresh*.

---

## 5. Casi limite

Ogni riga dice quanto valgono le quantità. «—» = indefinito, l'indicatore è nascosto (mai mostrato come 0). Scenario S salvo indicazione.

| # | Caso limite | Input | Atteso |
|---|---|---|---|
| E1 | **Inizio finestra** | S giovedì 10-01 09:00, x = 0, x₀ = 0 | E = 0, R = 22, idx —, proj 0, a nascosta, B = 4,55 (usato 0), q = s = 4,55, verdetto in linea, preliminare, T = 0,57, v — |
| E2 | **Ultimi minuti prima del reset** | S, ven 10-30 17:00, 7,9 h lavorate, x = 60, x₀ = 58 | R = 0,0125, proj 60,0, a = 14,7 > R (nessun consiglio), q = 40 (ρ 8,8, avanti), B = 42, **T = 0,57** (tetto 400 ignorato) |
| E3 | **Quota esaurita** | S, x = 100, x₀ = 90 | verdetto esaurita, a = 0, rem 0, q = 0, T = 0, B = 1,11 usato 13 (allarme di ritmo), nessun consiglio `autonomy`, nessuna spinta a consumare |
| E3b | **Oltre il limite** | S, x = 103 | come E3; valore mostrato 103, mai limitato; rimanente 0 |
| E4 | **Ancora nessun uso oggi** | S, x = x₀ = 60, nessuna attività | e = 0, E = 13, R = 9, used_today 0, B = 4,44, v = 0 o —, posizione misuratore 0, oggi non valutato |
| E5 | **Reset passato** (risposta vecchia, `resetsAt` < `now`) | finestra 5 ore, resetsAt 09:00, ora 10:00, x = 80 vecchio | nessuna quantità prospettica: hoursLeft 0, proiettato = 80, autonomia —, T —, verdetto solo da x (esaurita se ≥ 100, altrimenti nessun pacing); mostrato come non aggiornato fino alla prossima lettura fresca |
| E6 | **Data di reset mancante** | finestra mobile senza `resetsAt` | nessun pacing, nessun target, solo x |
| E6b | ciclo di fatturazione senza reset di finestra | rinnovo del provider 11-01 se c'è; altrimenti giorno manuale | vedi N-3..N-5 |
| E7 | **Orario disattivato** | S, disattivato | vedi T-10; idx = 1,04 per x = 60; T da H = 92,67 h: C = 0,43, s_h = 100/(31×8) = 0,40, T = 0,40 |
| E8 | **Giorno non lavorativo** | S, sab 10-24 10:00, x = x₀ = 70 | e = 0, B —, q = 6,0 (U = 5), limite dello slot 0: ogni uso oggi è `overBudget`, nessuna voce di valutazione (quota 0), la serie salta il giorno solo se non usato |
| E9 | **Inizio periodo ignoto** | ciclo `periodLength = null`, resetsAt 10-27, x = 72 | giorni al reset 7; idx, proj, a, B, q, T, valutazione **—**; verdetto nessun pacing; v mostrato senza target; consiglio `nearReset` solo entro 2 giorni |
| E10 | **Mezza giornata** | S, venerdì `half`, 10:00, 4 h lavorate | e = min(0,5, 4/8) = 0,5; R scende di conseguenza |
| E11 | **Primo giorno di storico** | nessun punto storico | base: 0 se il periodo è iniziato oggi, altrimenti il valore attuale (il consumo prima della prima lettura è ignoto) |
| E12 | **Reset durante il giorno** | x₀ = 60, ora 3,5 | used_today = 3,5; il delta del giorno è nullo (non attribuibile) |
| E13 | **Giorno senza letture** | app chiusa tutto il martedì, ultimo valore salvato lun 60, prima lettura di mer 64 | la base di mer = ultimo valore di lun (60), quindi il delta di mer è 4 e include l'uso di mar; mar non ha punto né barra |
| E14 | **Finestra count senza totale** | usato 12, totale nullo | utilizzo —, nessun pacing, mai scelta come critica rispetto a una con un valore |

---

## 6. Invarianti

- **I-1** Il target non supera mai il tetto: `T ≤ C`.
- **I-2** Il target non cresce mai perché il reset si avvicina: `T ≤ s_h`, e `T` non dipende da quante poche ore restano (solo `C` sì).
- **I-3** Un consiglio non chiede mai una riduzione del 100 % o più, né inferiore all'1 %; su una quota esaurita non si consiglia nulla.
- **I-4** Un valore mostrato non deriva mai da un altro valore arrotondato. Si arrotonda all'ultimo passaggio; un valore vivo confrontato con uno salvato porta prima alla precisione salvata (0,01 pp).
- **I-5** Un valore che non esiste è nascosto, non mostrato come 0 (niente storico → niente valutazione, non cinque stelle; niente uso → niente ritmo).
- **I-6** Nulla di prospettico viene inventato per un inizio periodo ignoto o un reset mancante/passato.
- **I-7** Oggi non è mai giudicato come un giorno completato (valutazione, picco, serie); il budget di oggi è fisso durante il giorno.
- **I-8** `E + R = N`; `0 ≤ e ≤ u(oggi)`; `U ≥ R`.
- **I-9** La serie, il colore di una barra del grafico e il budget con cui si confronta sono lo stesso flag.
- **I-10** La proiezione non è mai limitata; il rimanente non è mai negativo; l'utilizzo non è mai limitato per la visualizzazione.
- **I-11** I giorni di calendario sono locali.
- **I-12** Un solo scrittore per ogni pezzo di stato (§7).

---

## 7. Stato

Cosa è persistito, chi lo scrive, quando, con quale precisione. Tutto il resto di questo documento è **calcolato a ogni aggiornamento da questo stato e dalla risposta viva**, e mai salvato.

| Stato | Contenuto | Unico scrittore | Quando | Precisione | Letto da |
|---|---|---|---|---|---|
| `history.dailyUsage` | per account, finestra e giorno locale: `used` (ultimo valore del giorno), `dayStartUsed` (base) | l'aggiornamento (`recordDailyUsage`) | il punto del giorno a ogni aggiornamento **fresco**; `dayStartUsed` fissato una volta alla creazione del punto | 0,01 pp | delta, grafico, valutazione, ritmo recente, budget di oggi |
| regola di `dayStartUsed` | ultimo valore del giorno prima se dentro il periodo e non sceso di più di 1 pp; 0 se il periodo è iniziato dopo o c'è stato un reset; senza storico 0 se il periodo è iniziato oggi, altrimenti il valore attuale | `updateDailyPoint` (pura) tramite lo stesso scrittore | alla creazione del punto del giorno | 0,01 pp | B, used_today, intervallo di attività |
| `history.recentSamples` | `used` con timestamp per account e finestra | l'aggiornamento (`recordRecentSample`) | solo un valore **appena letto** | 0,01 pp | ritmo istantaneo, intervallo di attività |
| `history.lastGood` | ultima risposta grezza riuscita per account | il percorso di lettura | a una lettura riuscita | esatta | mostrata come non aggiornata se una lettura fallisce |
| `meta.notifiedToday` | flag `<account>:threshold:<day>`, `<account>:pace:<day>` | il passo di notifica | quando una notifica parte | — | lo stesso passo; si tengono solo i flag di oggi |
| Impostazioni account | orario, giorno di rinnovo manuale, host, … | Impostazioni (`settings:set`) | al salvataggio dell'utente | — | ogni calcolo |
| Cache insight locali | token per giorno, prima sessione del giorno | il risultato del processo di utilità | al massimo ogni pochi minuti | — | resa, causa, intervallo di attività |

Conservazione: punti giornalieri `retentionDays`; campioni tutto il giorno corrente e almeno 4 ore.

Regole: un calcolo **legge** lo stato e non lo scrive mai; lo snapshot inviato al renderer non è persistito; il renderer legge i valori e non li ricalcola (li formatta soltanto).

---

## 8. Casi di regressione (le issue che hanno portato qui)

| Issue | Cosa è andato storto | Caso | Atteso |
|---|---|---|---|
| #64 | budget/uso di oggi confrontati a precisioni diverse; il ripiego a percentuale intera di un provider letto come reset | B-2, B-3, B-4 | used_today 0; 0; 3,5 |
| #64 | il consiglio sulla valutazione dichiarava un rapporto diverso dal tooltip | R-1 + TP | `avgRatio` del consiglio = `avgRatio` della valutazione (1,26) |
| #72 | il target del misuratore arrivava a 331 %/h vicino al reset (quota rimanente su poche ore lavorative) | **G-12**, E2 | T = 0,57 (la quota equa), non 331; C = 331 è solo il tetto |
| #73 | il consiglio chiedeva «un ritmo inferiore del 100 %» su una quota esaurita | **TP-2**, TP-3, E3 | nessun consiglio |
| HYP#78 | una finestra mobile di 7 giorni veniva spostata a mezzanotte: la prima ora contava come giorno intero e le ultime 23 ore erano escluse | **T-15**: mer 14/10 23:00 → mer 21/10 23:00 locali; ora sab 17/10 18:38, orario disattivato | trascorsi **2,82** giorni, rimanenti **4,18**, al posto di 3,78 / 3,22; precisione completa 4058/1440 e 6022/1440 |
| #67 | indice di efficienza e stelle sostituiti poco dopo l'introduzione | R-1…R-6, P-6 | ridefiniti da #67 contro questa tabella |

---

## 9. Disallineamenti noti del codice attuale (input di #75)

Emersi scrivendo il modello; ognuno diventa un caso che fallisce e una voce del refactoring. **Nessuna correzione prima di #75** (vedi la regola della issue).

1. **Target = tetto** (`sustainableHourlyRate` restituisce `C`): G-7 dà 0,87 invece di 0,57, G-12 dà 331 invece di 0,57. Solo il ripiego sul calendario evita il caso a zero ore.
2. **Target con inizio periodo ignoto** è il tetto di calendario (crediti una tantum); il modello non ne ha (I-6).
3. **Il consiglio `autonomy`** non ha limiti sulla riduzione: TP-2 mostra 100 %, TP-4 0 %. È calcolato su autonomia e giorni lavorativi *arrotondati* (TP-1: 8,4 / 8,6), non su quelli esatti (I-4).
4. **Il rapporto del verdetto** è calcolato da `perUnit` e `idealPerUnit` già arrotondati a 0,01 (I-4): `ρ` ai limiti 0,95/1,05/0,5 può ribaltarsi. Il verdetto confronta `proj > 100` sul valore arrotondato a 0,1.
5. **Punto giornaliero scritto a ogni aggiornamento, anche non fresco** (il flag `fresh` vale solo per i campioni): il modello ha una sola regola, solo fresco (I-12).
6. **Reset passato**: `sustainableHourlyRate` risponde `0` invece di «indefinito»; il misuratore allora si legge come «sempre oltre».
7. **Finestra mobile senza `resetsAt`** ripiega sulla regola di rinnovo manuale come fine periodo; privo di senso per una finestra mobile (E6).
8. **Limiti dei giorni mobili troncati al giorno locale** (`startOfDay`): HYP#78 conta 3,78 trascorsi / 3,22 rimanenti invece di 2,82 / 4,18. Modello confermato (§1-2): conservare i timestamp mobili esatti e pesare la sovrapposizione di ogni giorno locale per la sua unità lavorativa, divisa per 24 ore; i cicli di fatturazione mantengono giorni locali interi. Implementato nel package Go `quotaperiod` (HYP#161); l’integrazione TypeScript rimane parte di HYP#75.
9. ~~**`resolveRenewalDate` con giorno 29-31** traboccava nel mese seguente~~ — risolto (#111, N-6..N-9).
10. **`budget.ts` mescola** pacing a giorni, misuratore a ore, gestione dello storico, grafico, insight e consigli in un solo file; il modello suggerisce la divisione usata da questo documento (§2 tempo, §3.2-3.8 pacing, §3.6/3.9 orario, §3.11-3.12 storico, §3.14-3.15 consigli).

---

## 10. Decisioni da confermare in revisione

- **D1** `T = min(C, s_h)` (la quota oraria equa limita il target). Alternativa: `T = C` con un minimo sulle ore rimaste. Il limite è l'unica regola che soddisfa I-2 per qualsiasi `H`.
- **D2** Nessun target con inizio periodo ignoto (i crediti una tantum perdono il target che hanno oggi).
- **D3** Una quota esaurita non mostra alcun consiglio, e la proiezione resta senza limite.
- **D4** I punti giornalieri si scrivono solo da dati freschi.
- **D5** Reset passato: quantità prospettiche nascoste fino a una lettura fresca.
- **D6** Soglie della valutazione e tetto 3×, banda ±5 %, 0,5×, rapporto di ritmo 1,5× e limite preliminare di 2 unità restano come sono (la taratura è #28, dopo l'uso reale).

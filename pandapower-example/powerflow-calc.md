# Lastflussberechnung (`run_powerflow.py`) — zwei Beschreibungen

Dieses Dokument beschreibt dieselbe Berechnung (`pp.runpp(net, algorithm="nr",
calculate_voltage_angles=True)` in `run_powerflow.py`, plus die
PTDF/LODF-Sensitivitäten in `sensitivity_analysis.py`) zweimal aus
unterschiedlicher Perspektive:

1. **Mathematisch/elektrotechnisch** (Gleichungen, Matrizen, Lösungsverfahren
   der Netzberechnung, wie in einem Lehrbuch der elektrischen Energietechnik).
2. **IT-/informatiknah** (Datenstrukturen, Algorithmus als Pseudocode/
   Kontrollfluss, Bezug zu den konkreten pandapower-DataFrames und CSV-Spalten
   dieses Projekts).

Jeweils zuerst **allgemein** (wie ein AC-Lastfluss grundsätzlich funktioniert),
danach **bezogen auf das konkrete Beispiel** `ReliCapGrid_Espheim`
(131 Busse, 191 Leitungen, 16 Trafos, 103 Lasten, 22 PV-Generatoren,
1–2 Slack), siehe `README.md` für die Modellierungsentscheidungen im Detail.

---

## 1. Mathematisch/elektrotechnische Notation

Konvention: Vektoren fett-klein ($\mathbf{v}$), Matrizen fett-groß
($\mathbf{M}$), $(\cdot)^{\mathsf T}$ Transponierte, $(\cdot)^{*}$ konjugiert
komplex, $\mathrm{diag}(\mathbf v)$ die Diagonalmatrix mit $\mathbf v$ auf der
Hauptdiagonalen, $j=\sqrt{-1}$. Alle Größen in p.u. (bezogen auf $S_{base}$,
$V_{base}$), wie in pandapower/PYPOWER üblich.

### 1.1 Allgemein: Netzmodell als Knoten-Admittanzmatrix

Ein Netz mit $n$ Bussen und $m$ Zweigen (Leitungen/Trafos) wird durch die
**Zweig-Inzidenzmatrix** $\mathbf{A}\in\{-1,0,1\}^{m\times n}$ beschrieben
(Zeile $l=(i,k)$: $A_{l,i}=+1$, $A_{l,k}=-1$, sonst $0$) sowie die
**Zweig-Admittanzmatrix** $\mathbf{Y}_{br}=\mathrm{diag}(y_1,\dots,y_m)\in
\mathbb{C}^{m\times m}$ mit $y_l=\dfrac{1}{r_l+jx_l}$ je Zweig. Daraus ergibt
sich die komplexe **Knoten-Admittanzmatrix** (Bus-Admittanzmatrix)

$$
\mathbf{Y} \;=\; \mathbf{A}^{\mathsf T}\,\mathbf{Y}_{br}\,\mathbf{A} \;+\; \mathbf{Y}_{sh}
\;=\; \mathbf{G} + j\mathbf{B} \;\in\; \mathbb{C}^{n\times n}
$$

$\mathbf{Y}_{sh}$ fasst Nebenelemente zusammen (Leitungsladeströme
$b_{ch}/2$, Trafo-Magnetisierungsadmittanz). $\mathbf Y$ ist **dünnbesetzt**
(sparse): $Y_{ik}\neq 0$ nur für über einen Zweig direkt verbundene Busse
$i,k$, $Y_{ii}$ = Summe aller an Bus $i$ anliegenden Admittanzen.

### 1.2 Allgemein: AC-Leistungsflussgleichungen

Sei $\mathbf V=(V_1,\dots,V_n)^{\mathsf T}\in\mathbb C^n$ der Spannungsvektor,
$V_i=|V_i|\,e^{j\theta_i}$. Die komplexe **Scheinleistung** an jedem Bus
ergibt sich vektoriell aus dem Ohmschen Gesetz $\mathbf I=\mathbf Y\mathbf V$:

$$
\mathbf S \;=\; \mathbf P + j\mathbf Q \;=\; \mathrm{diag}(\mathbf V)\,\big(\mathbf Y\,\mathbf V\big)^{*}
$$

komponentenweise in Polarkoordinaten (die für Newton-Raphson übliche Form,
mit $\theta_{ik}:=\theta_i-\theta_k$):

$$
P_i = \sum_{k=1}^{n} |V_i||V_k|\big(G_{ik}\cos\theta_{ik} + B_{ik}\sin\theta_{ik}\big)
$$

$$
Q_i = \sum_{k=1}^{n} |V_i||V_k|\big(G_{ik}\sin\theta_{ik} - B_{ik}\cos\theta_{ik}\big)
$$

Dies ist ein **nichtlineares algebraisches Gleichungssystem** in den
Zustandsgrößen $\boldsymbol\theta=(\theta_1,\dots,\theta_n)^{\mathsf T}$,
$|\mathbf V|=(|V_1|,\dots,|V_n|)^{\mathsf T}$.

**Busklassifizierung** (Randbedingungen, die das System eindeutig lösbar
machen — Partitionierung der Busmenge
$\mathcal N=\mathcal N_{slack}\,\dot\cup\,\mathcal N_{PV}\,\dot\cup\,\mathcal N_{PQ}$):

| Bustyp | vorgegeben | gesucht |
|---|---|---|
| Slack ($V\theta$) | $\|V_i\|$, $\theta_i=0$ (Referenz) | $P_i$, $Q_i$ |
| PV (Generator, spannungsgeregelt) | $P_i$, $\|V_i\|$ | $\theta_i$, $Q_i\in[Q_i^{min},Q_i^{max}]$ |
| PQ (Last) | $P_i$, $Q_i$ | $\theta_i$, $\|V_i\|$ |

Mit $n_{PV}=|\mathcal N_{PV}|$, $n_{PQ}=|\mathcal N_{PQ}|$ hat das System
genau $2n_{PQ}+n_{PV}$ Unbekannte und ebenso viele Gleichungen ($P$-Gleichung
für jeden Nicht-Slack-Bus, $Q$-Gleichung nur für PQ-Busse).

### 1.3 Allgemein: Newton-Raphson-Lösungsverfahren

Zustandsvektor
$\mathbf x=\begin{bmatrix}\boldsymbol\theta_{\mathcal N\setminus slack}\\
|\mathbf V|_{\mathcal N_{PQ}}\end{bmatrix}$, Mismatch-Funktion

$$
\mathbf f(\mathbf x) \;=\; \begin{bmatrix} \mathbf P^{spec} - \mathbf P^{calc}(\mathbf x) \\[2pt]
\mathbf Q^{spec} - \mathbf Q^{calc}(\mathbf x) \end{bmatrix}
\;=\; \begin{bmatrix} \Delta\mathbf P(\mathbf x) \\ \Delta\mathbf Q(\mathbf x) \end{bmatrix}
$$

Taylor-Linearisierung um $\mathbf x^{(k)}$ liefert die **Jacobi-Matrix**

$$
\mathbf J(\mathbf x) \;=\; \begin{bmatrix}
\dfrac{\partial \mathbf P}{\partial \boldsymbol\theta} & \dfrac{\partial \mathbf P}{\partial |\mathbf V|} \\[8pt]
\dfrac{\partial \mathbf Q}{\partial \boldsymbol\theta} & \dfrac{\partial \mathbf Q}{\partial |\mathbf V|}
\end{bmatrix}
\;=\; \begin{bmatrix} \mathbf J_{11} & \mathbf J_{12} \\ \mathbf J_{21} & \mathbf J_{22} \end{bmatrix}
$$

und das Iterationsschema ($\mathbf J$, $\Delta\mathbf x$ jeweils bei
$\mathbf x^{(k)}$ ausgewertet):

$$
\mathbf J\big(\mathbf x^{(k)}\big)\,\Delta\mathbf x^{(k)} \;=\; \mathbf f\big(\mathbf x^{(k)}\big),
\qquad \mathbf x^{(k+1)} = \mathbf x^{(k)} + \Delta\mathbf x^{(k)}
$$

bis $\big\lVert \mathbf f(\mathbf x^{(k)})\big\rVert_\infty < \varepsilon$
(Konvergenztoleranz, pandapower-Default $10^{-8}$ MW/MVAr). $\mathbf J$ wird
in jeder Iteration neu ausgewertet und das lineare System per (sparse)
LU-Zerlegung gelöst — **lokal quadratische Konvergenz** um die Lösung,
typischerweise 3–6 Iterationen. Aus $\mathbf x^{*}$ folgen alle
Ergebnisgrößen: komplexe Zweigströme/-flüsse
$S_{ik}=V_i\big(y_{ik}(V_i-V_k)\big)^{*}$, Verluste, Auslastung $I_{ik}/I_{max}$.

`calculate_voltage_angles=True` hält $\boldsymbol\theta$ als vollwertige
Zustandsgröße (statt der Vereinfachung $\boldsymbol\theta\equiv 0$) — nötig,
sobald Winkeldifferenzen zwischen mehreren Einspeisepunkten/Trafostufen
elektrisch relevant sind.

### 1.4 Bezogen auf `ReliCapGrid_Espheim`

- $n=131$ ($\mathbf Y\in\mathbb C^{131\times 131}$, sparse mit höchstens
  $2\cdot(191+16)$ Off-Diagonal-Einträgen), $m=207$ Zweige (191 Leitungen +
  16 Trafos).
- $|\mathcal N_{slack}|=1$ effektiver Referenzbus (beide
  `ExternalNetworkInjection`-Objekte liegen am selben 220-kV-Bus), $n_{PV}=22$
  (Synchronmaschinen, `RegulatingControl.mode = voltage`, Sollwert
  $|V_i|=1{,}0$ p.u., siehe README „Offene Punkte"), $n_{PQ}\approx 108$
  (103 `ConformLoad`-Lasten + lastfreie Durchgangsbusse mit $P_i=Q_i=0$).
  Damit hat $\mathbf x$ $130+108=238$ Komponenten.
- Konvergiertes Ergebnis (README Abschnitt 3): Spannungsband
  $\min_i|V_i^{*}|=0{,}844$, $\max_i|V_i^{*}|=1{,}111$ p.u., maximale
  Zweigauslastung $181{,}1\%$ (Zweig `26-30`), Slack-Einspeisung
  $P^{*}_{slack}=718{,}8\,\text{MW}$, $Q^{*}_{slack}=0{,}0\,\text{MVAr}$.
- Trafo-Kurzschlussimpedanz $z_l=r_l+jx_l$ (aus `vk_percent`, `vkr_percent`,
  auf OS-Seite bezogen) geht als gewöhnlicher Diagonaleintrag $y_l=1/z_l$ in
  $\mathbf Y_{br}$ ein — ohne Kernverluste (`pfe_kw = i0_percent = 0` in
  diesem Beispiel) ist ein Trafo in $\mathbf Y$ nicht von einer Leitung mit
  reiner Serienimpedanz zu unterscheiden.

### 1.5 Sensitivitäten (PTDF/LODF) — linearisiertes DC-Modell

**Anschauliche Erklärung, bevor die Herleitung folgt:**

- **PTDF** (Power Transfer Distribution Factor) beantwortet die Frage: *„Wenn
  an einem bestimmten Bus 1 MW mehr eingespeist bzw. entnommen wird (der
  Ausgleich erfolgt am Slack) — wie stark ändert sich dadurch der
  Wirkleistungsfluss auf einem bestimmten Zweig (Leitung/Trafo)?"* Die
  PTDF-Matrix $\mathbf H$ hat eine Zeile pro Zweig und eine Spalte pro Bus
  (ohne Slack); Werte liegen meist zwischen $-1$ und $+1$. Positiv heißt:
  mehr Einspeisung an diesem Bus erhöht den Fluss auf dem Zweig, negativ
  heißt: sie entlastet ihn. Praktischer Nutzen: Einspeisemanagement/
  Redispatch-Planung — an welchem Bus muss ich ansetzen, um eine bestimmte
  Leitung zu entlasten?
- **LODF** (Line Outage Distribution Factor) beantwortet die Frage: *„Wenn
  ein bestimmter Zweig $k$ komplett ausfällt — welcher Anteil seines
  bisherigen Flusses landet auf einem anderen Zweig $l$?"* (klassische
  n-1-Sicherheitsanalyse). Die LODF-Matrix ist quadratisch (Zeile und Spalte
  jeweils ein Zweig) und wird algebraisch direkt aus der PTDF-Matrix
  abgeleitet, ohne ein neues Gleichungssystem zu lösen. Werte nahe $\pm 1$
  bedeuten, dass Zweig $l$ (fast) den gesamten Fluss des ausgefallenen
  Zweigs $k$ übernimmt — typisch bei radial/eng gekoppelten Zweigpaaren.
  Werte $\to\pm\infty$ markieren eine **strukturelle Brücke**: Zweig $k$ ist
  die einzige Verbindung zwischen zwei Netzteilen, ohne ihn zerfällt das
  Netz in getrennte Teilnetze (0 % n-1-Redundanz an dieser Stelle).
- **Beide Kennzahlen sind linear** und hängen nur von Netztopologie und
  Impedanzen ab (nicht vom aktuellen AC-Lastfluss-Arbeitspunkt) — deshalb
  reicht dafür das unten hergeleitete, einfachere DC-Modell statt des vollen
  Newton-Raphson-AC-Lösers aus Abschnitt 1.3.

`sensitivity_analysis.py` verwendet das **DC-Lastflussmodell**: Näherungen
$\sin\theta_{ik}\approx\theta_{ik}$, $\cos\theta_{ik}\approx 1$,
$|V_i|\approx 1$ p.u. für alle $i$, Wirkverluste ($\mathbf G$) vernachlässigt.
Damit wird aus Abschnitt 1.2 die lineare Beziehung

$$
\mathbf P \;=\; \mathbf B'\,\boldsymbol\theta,
\qquad \mathbf B' = \mathbf A^{\mathsf T}\,\mathrm{diag}(b_1,\dots,b_m)\,\mathbf A
\ \text{(Slack-Zeile/-Spalte entfernt, } b_l=1/x_l\text{)}
$$

Der Zweigfluss
$\mathbf P_{br}=\mathrm{diag}(b_1,\dots,b_m)\,\mathbf A\,\boldsymbol\theta
=\mathrm{diag}(b_1,\dots,b_m)\,\mathbf A\,(\mathbf B')^{-1}\mathbf P$
liefert direkt die

- **PTDF-Matrix** (Power Transfer Distribution Factor,
  $\mathbf H\in\mathbb R^{m\times n}$):

$$
\mathbf H \;=\; \mathrm{diag}(b_1,\dots,b_m)\;\mathbf A\;(\mathbf B')^{-1},
\qquad H_{l,b} = \frac{\partial P_l}{\partial P_b}\Big|_{\text{Ausgleich am Slack}}
$$

- **LODF-Matrix** (Line Outage Distribution Factor,
  $\mathbf{LODF}\in\mathbb R^{m\times m}$), algebraisch aus $\mathbf H$ für
  Zweigausfall $k$ abgeleitet:

$$
\mathrm{LODF}_{l,k} \;=\; \dfrac{H_{l,k}}{1 - H_{k,k}} \quad (l \ne k),
\qquad \mathrm{LODF}_{k,k} = -1
$$

  $H_{k,k}$ ist hier der PTDF-Eintrag „Zweig $k$ auf sich selbst"
  (Sensitivität, die aus der Inzidenzmatrix-Projektion von $\mathbf H$
  folgt). $\mathrm{LODF}_{l,k}\to\pm\infty$ ($H_{k,k}\to 1$) markiert eine
  **strukturelle Brücke**: Zweig $k$ ist die einzige Verbindung, das Netz
  zerfällt ohne ihn in Teilnetze — $\mathbf B'$ wird für die Restkomponente
  singulär, sobald $k$ entfernt wird.
- (`pandapower.pypower.makePTDF`/`makeLODF` implementieren exakt diese beiden
  Formeln numerisch über `scipy.sparse` statt symbolisch.)

Konkret für `ReliCapGrid_Espheim`: $\mathbf H\in\mathbb R^{200\times 128}$,
$\mathbf{LODF}\in\mathbb R^{200\times 200}$ (200 Zweige nach
Konnektivitätsfilter, 128 Busse ohne Slack), 10 Zweige mit
$\mathrm{LODF}\to\pm\infty$ (strukturelle Brücken, u. a. `TieLine_EH-SD2/3`,
`LineEH-SD4`). Fokus auf den mit $181{,}1\%$ am höchsten ausgelasteten Zweig
`26-30`: größte $|H_{26\text{-}30,\,b}|\approx 0{,}70$ an Bus
`CONNECTIVITY_NODE306` (negatives Vorzeichen: zusätzliche Einspeisung an $b$
entlastet den Zweig); $\mathrm{LODF}_{26\text{-}30,\,trafo:26\text{-}25}
\approx +1{,}00$ (nahezu vollständige Flussübernahme bei Ausfall des
Nachbartrafos).

---

## 2. IT-/informatiknahe Beschreibung

### 2.1 Allgemein: Datenmodell und Algorithmus

Ein pandapower-Netz (`net = pp.create_empty_network()`) ist im Kern ein
**Bündel von pandas-DataFrames** (eine Tabelle pro Elementtyp: `net.bus`,
`net.line`, `net.trafo`, `net.load`, `net.gen`, `net.ext_grid`, …), jede Zeile
ein Objekt mit einer Integer-ID (dem DataFrame-Index). `pp.runpp(net, ...)`
läuft intern in mehreren Schritten ab (vereinfachtes Pseudocode-Schema):

```
1. build_ppc(net)          # DataFrames -> internes "PYPOWER case" (ppc):
                            #   ppc["bus"]    : ndarray, pro Zeile ein Bus
                            #   ppc["branch"] : ndarray, pro Zeile Leitung/Trafo (r,x,b,tap,...)
                            #   ppc["gen"]    : ndarray, pro Zeile Slack/PV-Generator
2. makeYbus(ppc)            # baut die sparse Admittanzmatrix Y (scipy.sparse, CSR/CSC)
3. newtonpf(Ybus, Sbus, V0, ref, pv, pq, ...)   # Newton-Raphson-Iterationsschleife:
     while not converged and iter < max_iter:
         mismatch = compute_mismatch(V, Ybus, Sbus, pv, pq)
         J = makeJacobian(Ybus, V, pv, pq)       # scipy.sparse Jacobi-Matrix
         dx = scipy.sparse.linalg.spsolve(J, mismatch)   # lineares Gleichungssystem lösen
         V = update_voltage(V, dx, pv, pq)
         converged = norm(mismatch, inf) < tol
4. extract_results(ppc, V)  # Rückrechnung: Zweigflüsse, Verluste, Auslastung
5. ppc_to_net(ppc, net)     # Ergebnisse zurück in net.res_bus / res_line / res_trafo / ...
```

Kernpunkt für Informatiker: Der **Zustandsvektor** `x` (Spannungswinkel +
-beträge) wird durch eine **Fixpunktiteration mit lokaler quadratischer
Konvergenz** (Newton-Verfahren) bestimmt, nicht durch einen geschlossenen
Lösungsalgorithmus — jede Iteration kostet eine (sparse) lineare
Gleichungssystemlösung (`O(n^1.x)` bei dünnbesetzten Matrizen mit LU-Zerlegung,
nicht `O(n^3)` wie bei dichten Matrizen). Die Datenstruktur `Ybus` ist
**sparse**, weil jeder Bus real nur mit wenigen Nachbarn (angeschlossene
Leitungen/Trafos) verbunden ist — der Speicher- und Rechenaufwand skaliert
mit der Anzahl der Kanten (Leitungen/Trafos), nicht mit `n²`.

### 2.2 Bezogen auf `run_powerflow.py`/`ReliCapGrid_Espheim`

Konkreter Bezug zu diesem Projekt:

- **`build_net(data_dir)`** liest 6 CSV-Dateien (`buses.csv`, `lines.csv`,
  `trafos.csv`, `loads.csv`, `gens.csv`, `ext_grid.csv`, erzeugt von
  `extract_cim_to_csv.py` aus dem CIM/CGMES-Rohmodell) und baut daraus über
  `pp.create_bus/_line_from_parameters/_transformer_from_parameters/_load/
  _gen/_ext_grid(...)` die pandapower-DataFrames auf. Jede CSV-Zeile wird 1:1
  zu einer DataFrame-Zeile; `bus_idx: dict[str, int]` mappt die CIM-
  `bus_id`-Strings (ursprünglich `TopologicalNode`-mRIDs) auf pandapowers
  interne Integer-Busindizes — dieselbe Rolle wie eine ID-Registry beim
  Auflösen von Fremdschlüsseln.
- **Konkrete Tabellengrößen für dieses Beispiel** nach dem Aufbau:
  `net.bus` 131 Zeilen, `net.line` 191, `net.trafo` 16, `net.load` 103,
  `net.gen` 22, `net.ext_grid` 1–2 (Fallback-Logik: wenn `n_ext == 0`, wird
  der Bus mit `vn_kv.idxmax()` als synthetischer Slack ergänzt — im
  tatsächlichen Lauf nicht nötig, da beide `ExternalNetworkInjection`-Objekte
  vorhanden sind).
- **`pp.runpp(net, algorithm="nr", calculate_voltage_angles=True)`** ist der
  eigentliche Aufruf, der intern die in 2.1 skizzierte Newton-Raphson-
  Pipeline auf genau diesen DataFrames ausführt. `algorithm="nr"` wählt den
  Solver-Pfad `pandapower.pf.runpp_pf.newtonpf` (statt z. B.
  `algorithm="gs"`/`"fdbx"`); `calculate_voltage_angles=True` verhindert, dass
  pandapower die (in Verteilnetzen oft zulässige) Vereinfachung `θ ≈ 0`
  vornimmt — hier nötig, weil das Netz Transformator-Ketten und zwei
  Einspeisepunkte am selben Bus enthält.
- **Fehlerbehandlung**: `pp.runpp` wirft `LoadflowNotConverged`, wenn die
  Newton-Iteration nicht innerhalb der Toleranz/Iterationsgrenze konvergiert
  (`run_powerflow.py` fängt das als generisches `Exception` ab und beendet
  mit Exitcode 1) — in der Entwicklungshistorie dieses Projekts zweimal
  aufgetreten (unverbundene Inseln ohne Slack; Generatoren ohne
  Spannungsregelung als `sgen` statt `gen`, siehe README Abschnitt 4).
- **Ergebnis-Rückgabe**: `net.res_bus`, `net.res_line`, `net.res_trafo`,
  `net.res_gen`, `net.res_ext_grid` sind neue DataFrames mit demselben Index
  wie die Eingabetabellen (`res_bus.index == bus.index` usw.) — Zugriff über
  `net.res_line.join(net.line[["name"]])` in `run_powerflow.py` ist ein
  gewöhnlicher pandas-Join über den gemeinsamen Index, keine Neuberechnung.
- **`sensitivity_analysis.py`** ruft stattdessen
  `pandapower.pypower.makePTDF`/`makeLODF` auf dem internen `ppc`
  (`net._ppc`, nach einem vorherigen `pp.runpp`-Lauf verfügbar) auf — diese
  Funktionen arbeiten direkt mit `numpy`-Arrays/`scipy.sparse`-Matrizen (nicht
  mit den pandas-DataFrames) und geben die PTDF-Matrix (`200×128`
  `ndarray`/`DataFrame`) sowie LODF-Matrix (`200×200`) zurück, die das Skript
  dann als `docs/ptdf.csv`/`docs/lodf.csv` exportiert (`DataFrame.to_csv`).
  Die Zeilen-/Spaltenbeschriftung (Zweig-/Busnamen statt reiner Indizes) wird
  im Skript selbst ergänzt, da die PYPOWER-Rohfunktionen nur mit
  Integer-Indizes arbeiten.
# ReliCapGrid_Espheim -- Netzberechnung mit pandapower

Dieser Ordner stellt eine eigenständige Netzberechnung auf Basis des CGMES-2.4.15
Beispieldatensatzes `examples/cgmes/ReliCapGrid_Espheim/` zusammen. Die
Eingabedaten für pandapower werden **direkt aus den rohen CIM/CGMES-XML-Dateien**
extrahiert (`extract_cim_to_csv.py`, mit `lxml`) -- **nicht** über den in
pandapower eingebauten CGMES-Konverter (`pandapower.converter.cim`). Das war
eine explizite Entscheidung: die vier relevanten CIM-Klassen
(`ACLineSegment`, `PowerTransformerEnd`, `ConformLoad`, `SynchronousMachine`,
`ExternalNetworkInjection`) liefern in diesem Datensatz bereits vorberechnete
Gesamtwerte (r/x/bch je Segment, nicht nur Katalog-Werte pro Kilometer), so
dass eine schlanke, nachvollziehbare Handextraktion ausreicht und einfacher zu
prüfen ist als der allgemeine Konverter.

## Ordnerinhalt

| Datei/Ordner | Zweck |
|---|---|
| `extract_cim_to_csv.py` | Parst EQ/SSH/TP-Profile und schreibt `data/*.csv` |
| `run_powerflow.py` | Baut aus den CSVs ein pandapower-Netz und führt `pp.runpp()` aus |
| `plot_network.py` | Erzeugt `docs/network_diagram.png` (Topologie + Spannungsergebnis) |
| `sensitivity_analysis.py` | Berechnet PTDF/LODF-Sensitivitäten (`docs/ptdf.csv`, `docs/lodf.csv`) |
| `data/` | Generierte CSV-Eingabedaten (buses/lines/trafos/loads/gens/ext_grid) -- **abgeleitet, klein, unkritisch, daher versioniert** |
| `docs/` | Diese Dokumentation, Ergebnis-Rohausgabe, Netzdiagramm |
| `Dockerfile`, `requirements.txt` | Für die Ausführung via Docker-Image |

Die eigentlichen CIM-Quelldaten unter `examples/cgmes/ReliCapGrid_Espheim/`
werden **nicht** in diesen Ordner kopiert (sie sind ohnehin gitignored) --
`extract_cim_to_csv.py` liest sie per relativem Pfad (`../examples/...`).

## 1. Datenquelle und Netzmodell

`ReliCapGrid_Espheim` ist ein CGMES-2.4.15-Datensatz (Namespace `CIM100`) im
Node-Breaker-Format. Die Generator-Namen (`TannrsCk SM`, `Muskngum SM`,
`Glen Lyn SM`, `CabinCrk SM`, ...) entsprechen den bekannten Erzeugereinheiten
des **IEEE-118-Bus-Testsystems** -- dieser Datensatz ist also im Kern eine
CGMES-Kodierung des IEEE-118-Systems (Übertragungsnetz-Ebene, nicht MS/NS).

**Rohdaten (vollständig, vor Konnektivitäts-Filterung):**

| Element | Anzahl |
|---|---|
| Substation | 107 |
| Bay | 378 |
| VoltageLevel | 121 (4 Spannungsebenen: 33/132/220/400 kV) |
| TopologicalNode (aus TP-Profil) | 177 |
| Breaker / Disconnector | 440 / 856 |
| ACLineSegment | 191 |
| PowerTransformer (2-Wicklung) | 17 |
| ConformLoad | 104 |
| SynchronousMachine | 25 |
| ExternalNetworkInjection | 2 |

### Modellierungsentscheidungen

- **Busse = `TopologicalNode`, nicht `ConnectivityNode`.** Das TP-Profil
  enthält bereits die vom Netzbetreiber/Tool durchgeführte
  Schalter-Kollabierung (`Terminal.TopologicalNode`) -- ein Bus in der
  Netzberechnung entspricht daher genau einem `TopologicalNode`. Eigene
  Nullwiderstands-Reduktion über Breaker/Disconnector war nicht nötig, da die
  Quelldaten das bereits korrekt vorwegnehmen.
- **Nennspannung je Bus** wird aus dem `VoltageLevel`-Namen abgeleitet (z. B.
  `GON220KV` -> 220 kV; kein `BaseVoltage`-Objekt im EQ-Profil vorhanden --
  reine Boundary-Referenz ohne Auflösung). Für die wenigen Busse, deren
  `VoltageLevel`-Name kein Spannungsmuster trägt (Generatorklemmen,
  Grenzknoten), wird die Spannung stattdessen von einem angeschlossenen
  `PowerTransformerEnd.ratedU` bzw. über eine Nachbarschaftspropagierung
  entlang von `ACLineSegment`-Kanten übernommen (Leitungen ändern die
  Spannungsebene nie). Damit sind alle 177 Busse eindeutig einer der vier
  Spannungsebenen zugeordnet.
- **Transformatoren**: `PowerTransformerEnd.r/x` beider Wicklungen werden auf
  die Oberspannungsseite umgerechnet und zu einer Kurzschlussimpedanz
  zusammengefasst (`vk_percent`/`vkr_percent`), wie es
  `create_transformer_from_parameters` erwartet.
- **Synchronmaschinen als spannungsgeregelte PV-Generatoren (`pp.create_gen`),
  nicht als `sgen` mit fester Blindleistung.** Die Quelldaten markieren
  `RegulatingControl.mode = voltage` für diese Maschinen -- ohne
  Spannungsregelung (freies Q innerhalb `minQ`/`maxQ`) konvergierte der
  Lastfluss nicht (siehe Abschnitt "Bekannte Probleme & Lösung" unten).
- **`ExternalNetworkInjection` = Slack (`ext_grid`).** Das ist die von CGMES
  vorgesehene Konstruktion für "Rest des Netzes" -- keine freie Wahl.
  `TopologicalIsland.AngleRefTopologicalNode` (die explizite CGMES-Referenz
  für den Winkel-Referenzknoten) fehlt in diesem Datensatz; die zwei
  `ExternalNetworkInjection`-Objekte sind die einzigen sinnvollen
  Slack-Kandidaten und liegen beide am selben 220-kV-Bus.
- **Leitungs-Grenzströme (`max_i_ka`)** stammen aus
  `CurrentLimit.normalValue` (Ampere), verknüpft über
  `OperationalLimitSet.Terminal -> Terminal.ConductingEquipment`, gefiltert
  auf `OperationalLimitType.kind = patl` (Permanent Admissible Transmission
  Loading, d. h. Dauergrenzstrom, nicht der kurzzeitige Notfallwert `tatl`).
  Nur 11 von 191 Leitungen haben einen im Datensatz hinterlegten `patl`-Wert;
  die übrigen erhalten einen Platzhalter von 1.0 kA (siehe Einschränkungen
  unten) -- entsprechend sind deren `loading_percent`-Werte nicht belastbar.
- **Konnektivitäts-Filter**: 46 der 177 `TopologicalNode` sind über keine
  `ACLineSegment`/`PowerTransformer`-Kante mit einem `ExternalNetworkInjection`
  verbunden (45 Ein-Knoten-Inseln + Nebenkomponenten). Ohne Pfad zu einer
  Einspeisung kann kein Lastfluss diese Busse versorgen; sie werden vor dem
  Aufbau des pandapower-Netzes verworfen (dokumentiert statt stillschweigend
  geraten). Übrig bleiben **131 Busse** in der Hauptkomponente.

## 2. Ausführung

```powershell
cd pandapower
python extract_cim_to_csv.py     # erzeugt data/*.csv aus den CIM-Rohdaten
python run_powerflow.py          # Lastfluss, Ergebnisse auf der Konsole
python plot_network.py           # docs/network_diagram.png
```

Oder via Docker (siehe `Dockerfile`):

```powershell
docker build -t jag-pandapower .
docker run --rm jag-pandapower
```

## 3. Ergebnisse der Netzberechnung (Stand dieser Doku)

AC-Lastfluss (Newton-Raphson, `pp.runpp`), 131 Busse / 191 Leitungen /
16 Transformatoren / 103 Lasten / 22 PV-Generatoren / 2 Slack-Einspeisungen
(beide am selben 220-kV-Bus):

| Größe | Wert |
|---|---|
| Gesamtlast | P = 4332.3 MW, Q = 1072.5 MVAr |
| Gesamterzeugung (Generatoren) | P = 3868.7 MW, Q = 1163.1 MVAr |
| Netzeinspeisung (Slack) | P = 718.8 MW, Q = 0.0 MVAr |
| Spannungsband | 0.844 -- 1.111 p.u. |
| Maximale Leitungsauslastung | 181.1 % |

Die vollständige Konsolenausgabe (alle Bus-, Leitungs-, Trafo- und
Generator-Ergebnisse) liegt in [`docs/run_output.txt`](docs/run_output.txt).

### Einordnung der Ergebnisse

- Last (4332 MW) und Erzeugung (3869 MW) liegen nahe beieinander -- der Slack
  deckt nur die Differenz (719 MW), plausibel für ein Übertragungsnetz mit
  eigener Erzeugung.
- Das Spannungsband (0.84--1.11 p.u.) ist breiter als in der Betriebspraxis
  üblich (meist 0.95--1.05 p.u.). Grund: alle 22 Generatoren wurden mit einem
  **pauschalen Spannungssollwert von 1.0 p.u.** modelliert, da die
  tatsächlichen `RegulatingControl.targetValue`-Sollwerte aus den Quelldaten
  noch nicht ausgewertet werden (offener Punkt, siehe unten).
- Die maximale Leitungsauslastung von 181 % ist mit Vorsicht zu lesen: nur 11
  von 191 Leitungen haben einen echten `patl`-Grenzstrom aus den CGMES-Daten;
  der Rest nutzt einen Platzhalter (1.0 kA). Bezogen nur auf die 11 Leitungen
  mit echtem Grenzwert ist die Aussage belastbar, für die übrigen nicht.

## 4. Bekannte Probleme & Lösungen (Entwicklungshistorie)

1. **Lastfluss konvergierte zunächst nicht.** Ursache: 46 Busse waren über
   keine Leitung/Trafo mit einer Einspeisung verbunden (Inseln ohne Slack) --
   behoben durch den Konnektivitäts-Filter (Abschnitt 1).
2. **Danach weiterhin keine Konvergenz.** Ursache: die 22 Synchronmaschinen
   wurden zunächst als `sgen` mit fixem Q eingelesen, ohne die im CIM
   hinterlegte Spannungsregelung. Nach Umstellung auf `pp.create_gen`
   (PV-Knoten, Q frei innerhalb `minQ`/`maxQ`) konvergiert der Lastfluss.
   **Der Slack-Knoten selbst war nie das Problem** -- er ist durch die beiden
   `ExternalNetworkInjection`-Objekte im CIM eindeutig vorgegeben.

## 5. Offene Punkte / bekannte Einschränkungen

- **Generator-Spannungssollwerte**: `RegulatingControl.targetValue` (falls im
  SSH-Profil vorhanden) wird noch nicht ausgelesen; alle Generatoren laufen
  aktuell mit `vm_pu = 1.0`. Das dürfte das breite Spannungsband erklären.
- **`max_i_ka`-Platzhalter**: 180 von 191 Leitungen haben keinen `patl`-Wert
  im Datensatz und erhalten 1.0 kA -- deren `loading_percent` ist nicht
  aussagekräftig.
- **Kein reales Geo-Layout**: `docs/network_diagram.png` nutzt ein
  automatisches Feder-Layout (`pandapower.plotting.create_generic_coordinates`,
  über `igraph`); Bus-Positionen entsprechen **nicht** echten Koordinaten (im
  EQ-Profil sind keine `Location`/`PositionPoint`-Objekte für dieses Netz
  vorhanden).
- **Trafo-Kernverluste** (`pfe_kw`, `i0_percent`) sind auf 0 gesetzt, da die
  Quelldaten dafür keine Werte liefern -- Ergebnis daher ohne
  Leerlaufverluste.

## 6. Sensitivitätsanalyse (PTDF/LODF)

Zusätzlich zur reinen Lastflussrechnung berechnet `sensitivity_analysis.py`
lineare Sensitivitäten auf Basis der (DC-approximierten) Netz-Admittanzmatrix
-- über `pandapower.pypower.makePTDF`/`makeLODF`, dieselben PYPOWER-Routinen,
auf denen pandapower selbst aufbaut. Ausführung:

```powershell
python sensitivity_analysis.py
```

Ergebnisse werden auf der Konsole ausgegeben und zusätzlich als vollständige
Matrizen nach `docs/ptdf.csv` (200 Zweige x 128 Busse) und `docs/lodf.csv`
(200 x 200) geschrieben. Die vollständige Konsolenausgabe liegt in
[`docs/sensitivity_output.txt`](docs/sensitivity_output.txt).

Zwei Größen werden unterschieden (die AC-Jacobi-Matrix aus dem letzten
Newton-Raphson-Schritt, `net._ppc["internal"]["J"]`, ist ebenfalls verfügbar
und wird nur als Referenzzeile ausgegeben -- der Fokus liegt hier bewusst auf
PTDF/LODF, da diese direkt für "was passiert, wenn..."-Fragen zur
Leitungsauslastung nutzbar sind):

- **PTDF** (Power Transfer Distribution Factor): wie stark sich der
  Wirkleistungsfluss auf einem Zweig (Leitung/Trafo) ändert, wenn an einem
  Bus 1 MW Einspeisung/Last hinzukommt (gegenüber dem Slack ausgeglichen).
- **LODF** (Line Outage Distribution Factor): wie stark sich der Fluss auf
  einem anderen Zweig ändert, wenn ein bestimmter Zweig ausfällt (n-1). Werte
  nahe ±1.0 bedeuten "übernimmt (fast) den gesamten Fluss des Ausfalls";
  ±∞ (in den Rohmatrizen als `-inf`/`inf`, im Konsolenoutput als eigene
  "Structural bridges"-Liste ausgewiesen) bedeutet: der ausgefallene Zweig
  ist eine **strukturelle Brücke** -- ohne ihn zerfällt das Netz in
  Teilnetze, es gibt schlicht keinen Alternativpfad (0 % n-1-Redundanz an
  dieser Stelle). Von den 200 Zweigen sind **10** solche Brücken (v. a.
  die Kuppelleitungen `TieLine_EH-SD2/3`, `LineEH-SD4`, `EH_line1` sowie
  einzelne Radialstichleitungen `12-117`, `71-73`, `110-111`, `110-112`,
  `L-356722699`, `trafo:86-87`).

### Fokus: die überlastete Leitung (181 % Auslastung)

Die im Lastfluss mit 181.1 % am höchsten ausgelastete Leitung ist **`26-30`**.
`sensitivity_analysis.py` wertet für genau diese Leitung PTDF und LODF gezielt
aus (Abschnitt "Focus: overloaded line" in der Konsolenausgabe):

- **PTDF (wer beeinflusst die Auslastung von `26-30` am stärksten?)**: die
  zehn einflussreichsten Busse liegen alle im Bereich **-0.66 bis -0.70**,
  angeführt von `CONNECTIVITY_NODE306` (-0.6997). Ein negatives Vorzeichen
  heißt: zusätzliche Einspeisung/Last an diesem Bus **entlastet** `26-30`
  (Fluss sinkt), Lastreduktion an diesem Bus würde die Überlast dagegen
  **verschärfen**. Da alle zehn Werte ähnlich groß und negativ sind, hängt
  die Auslastung von `26-30` nicht an einem einzelnen Knoten, sondern an
  einer ganzen Gruppe elektrisch eng gekoppelter Busse auf derselben Seite
  des Netzes -- ein gezieltes Gegenmaßnahmen-Ziel (z. B. Einspeisemanagement
  an nur einem Bus) hätte daher nur begrenzten Hebel; wirksamer wäre eine
  Maßnahme, die mehrere dieser Busse gemeinsam adressiert (z. B.
  Redispatch am vorgelagerten Trafo/Generator statt an einem einzelnen Bus).
- **LODF, Spalte `26-30` (was passiert mit anderen Leitungen, wenn `26-30`
  selbst ausfällt?)**: `trafo:26-25` übernimmt praktisch 100 % des
  Flusses (LODF = +1.0000, die beiden hängen offenbar radial zusammen),
  gefolgt von `23-25` (-0.62), `trafo:30-17` (-0.60) und `25-27` (+0.38).
  Ein Ausfall von `26-30` würde also den vorgelagerten Transformator
  `26-25` sofort in eine vergleichbare Überlast treiben.
- **LODF, Zeile `26-30` (welcher andere Ausfall verschärft die
  Überlast auf `26-30` am meisten?)**: an erster Stelle wieder
  `trafo:26-25` (+1.00 -- Totalausfall würde den kompletten Fluss auf
  `26-30` umlenken), danach `trafo:Aaa(1)` (+0.58), `23-25` (-0.54),
  `trafo:Aac` (+0.50) und `23-24` (+0.44). Das bestätigt: `26-30` und der
  Transformator `26-25` bilden einen gemeinsamen kritischen Pfad -- fällt
  einer der beiden aus, wird der jeweils andere zusätzlich belastet. Da
  bereits im Normalbetrieb (n-0) 181 % Auslastung vorliegen, würde jeder
  dieser n-1-Fälle die Leitung `26-30` (bzw. ihren Nachbarn) hier in eine
  noch deutlich stärkere Überlast treiben.

**Einordnung:** Da für `26-30` kein realer `patl`-Grenzstrom aus den
CGMES-Daten vorliegt (Platzhalter 1.0 kA, siehe Abschnitt 5), ist der absolute
Auslastungswert (181 %) nicht belastbar -- die PTDF/LODF-*Sensitivitäten*
selbst sind davon aber unabhängig korrekt (sie hängen nur von Impedanzen und
Netztopologie ab, nicht vom Grenzstrom), zeigen also unabhängig vom
tatsächlichen Grenzwert, welche Busse/Zweige für diese Leitung relevant sind.

## 7. Netzdiagramm

![Netzdiagramm](docs/network_diagram.png)

Rotes Quadrat = Slack (`ExternalNetworkInjection`), grünes Dreieck =
Synchronmaschine, Farbe = Busspannung in p.u. (rot = niedrig, grün = hoch).
Layout ist ein automatisches Feder-Layout, keine geografischen Koordinaten.

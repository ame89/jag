# jag

## Umgebungsvariablen (`cmd/phase2check`, teilweise auch `cmd/hjsonimport`)

Der Phase-2-Testtreiber `cmd/phase2check` (Import + Container/Terminal/Circuit-Aufbau
gegen eine CIM/CGMES/NSC-Beispieldatei oder ein ganzes Verzeichnis) liest folgende
Umgebungsvariablen, um die Verarbeitung zu steuern, ohne den Code anzufassen:

| Variable | Wirkung | Default |
|---|---|---|
| `JAG_DB_PATH` | Pfad der SQLite-Datei, in die importiert wird (bewusst eine echte Datei, nicht `:memory:`, damit Timings echte Disk-I/O widerspiegeln). Wird bei jedem Lauf vorher gelöscht (frischer Import). | `phase2check.db` |
| `JAG_FORCE_NSC` | `1` erzwingt den NSC-Dialekt-Import (`phase1.RunNSCFiles`) auch für ein Verzeichnis, das nur `.xml`- (nicht `.rdf`-)Dateien enthält, z. B. `example_as_cim.xml`. Ohne diese Variable entscheidet die Dateiendung (`.rdf`-Dateien im Verzeichnis ⇒ NSC). | unset (Endungs-Heuristik) |
| `JAG_CHUNK_SIZE` | Cursor-Batch-Größe (`staging.Store.GetByClass`-Limit) für alle klassenweisen Scans innerhalb eines Pass-A-Batches bzw. in Pass B (z. B. Substation-/Building-Paging, ACLineSegment-/Junction-Scans, die abschließenden flag-basierten Vollständigkeitsprüfungen). Größer = weniger DB-Roundtrips; seit dem Pass-A/B-Umbau ist dies **nicht** mehr der RAM-bestimmende Parameter (das ist `JAG_STATION_BATCH_SIZE`, s. u.) — nur noch eine reine DB-Roundtrip-Effizienz-Stellschraube. | `2000` |
| `JAG_STATION_BATCH_SIZE` | Anzahl Substation-/Building-Wurzeln pro Pass-A-Batch (`common.RunPassA`). Dies ist der eigentliche RAM-Begrenzer der Pipeline: der Node-/Edge-/Attribut-/Geometrie-Fußabdruck eines Batches skaliert mit dieser Zahl, nicht mit der Gesamtmodellgröße. | `1000` (`common.DefaultStationBatchSize`) |
| `JAG_STATION_WORKERS` | Anzahl paralleler Pull-Pool-Worker-Goroutinen in Pass A (`common.RunPassA`) — jeder Worker verarbeitet nacheinander ganze Batches (siehe `JAG_STATION_BATCH_SIZE`) über `ProcessStationBatch`. | `4` (`common.DefaultPassAWorkers`) |
| `JAG_PASS_B_WORKERS` | Anzahl paralleler Worker-Goroutinen für Pass B's ACLineSegment-Ketten-Build-Schritt (`common.RunPassB`/`discoverACLineChainsStreaming`). Die Ermittlung der Ketten-Zugehörigkeit selbst läuft bewusst einzelsträngig (Korrektheit hat Vorrang, siehe Kommentar in `acline_streaming.go`); nur der reine CPU-Build-Schritt pro bereits ermittelter Kette (Container-ID, Name, Node-/Edge-Aufbau) wird parallelisiert. Deckungsgleich mit `JAG_STATION_WORKERS`'s Default gehalten. | `4` (`common.DefaultPassBWorkers` = `common.DefaultPassAWorkers`) |
| `JAG_PASS_B_BATCH_SIZE` | Analog zu `JAG_STATION_BATCH_SIZE`, aber für Pass B: Anzahl bereits ermittelter ACLineSegment-Ketten (physische Kabeltrassen), die in einem Batch gebaut, persistiert und wieder verworfen werden (`common.RunPassB`/`discoverACLineChainsStreaming`'s Batch-Modus). Ein Lasttest (lasttest-500, 2026-07-18/19) zeigte, dass Pass B's RAM-Spitze mit der Gesamtzahl seiner Gruppen/Container skaliert, unabhängig von `JAG_STATION_BATCH_SIZE` (Pass B las diese Variable nie) — dieser eigene Batch-Größen-Regler ist die Behebung dafür. | `1000` (`common.DefaultPassBBatchSize` = `common.DefaultStationBatchSize`) |
| `JAG_CPU_PROFILE` | Pfad, unter dem ein `pprof`-CPU-Profil des gesamten Laufs geschrieben wird. | unset (kein Profil) |

**`cmd/hjsonimport`** (der Fachmodell-HJSON-Gegenstück-Treiber, siehe Konzept.md's
"HJSON Fachmodell"-Abschnitt) liest exakt dieselben sechs Variablen `JAG_DB_PATH`,
`JAG_CHUNK_SIZE`, `JAG_STATION_BATCH_SIZE`, `JAG_STATION_WORKERS`, `JAG_PASS_B_WORKERS`
und `JAG_PASS_B_BATCH_SIZE` mit identischen Defaults (nur der Default-Dateiname für
`JAG_DB_PATH` unterscheidet sich: `hjsonimport.db` statt `phase2check.db`) — `JAG_FORCE_NSC`
(HJSON hat keine CIM/CGMES/NSC-Dialekterkennung) und `JAG_CPU_PROFILE` (kein CPU-Profiling)
gelten dort nicht. `cmd/hjsonexport` liest keine `JAG_*`-Variablen; es wird ausschließlich
über Positionsargumente (`<db-path> <output-root> [default-netzregion]`) gesteuert.

### PostgreSQL-Backend (`cmd/phase2check`)

`cmd/phase2check` unterstützt seit dem PostgreSQL-Persistenz-Backend (`internal/postgres`,
Ports-&-Adapters-Gegenstück zu `internal/sqlite`) auch PostgreSQL statt SQLite als
Ziel-Datenbank. Standardmäßig (`JAG_BACKEND` unset) ändert sich nichts — es wird weiterhin
die SQLite-Datei aus `JAG_DB_PATH` verwendet.

| Variable | Wirkung | Default |
|---|---|---|
| `JAG_BACKEND` | `postgres` schaltet auf das PostgreSQL-Backend um. Jeder andere Wert (inkl. unset) behält das bisherige SQLite-Verhalten unverändert bei. | unset (SQLite) |
| `JAG_POSTGRES_DSN` | Vollständiger PostgreSQL-Connection-String/-URL (z. B. `postgres://jag:jag@localhost:5432/jag?sslmode=disable`), wird unverändert übernommen. Ist diese Variable gesetzt, werden alle anderen `JAG_POSTGRES_*`-Variablen ignoriert. | unset |
| `JAG_POSTGRES_HOST` | Hostname des PostgreSQL-Servers (nur relevant, wenn `JAG_POSTGRES_DSN` nicht gesetzt ist). | `localhost` |
| `JAG_POSTGRES_PORT` | Port des PostgreSQL-Servers. | `5432` |
| `JAG_POSTGRES_USER` | PostgreSQL-Benutzername. | `jag` |
| `JAG_POSTGRES_PASSWORD` | PostgreSQL-Passwort. | `jag` |
| `JAG_POSTGRES_DB` | Name der Datenbank. Bewusst nur ein Vorschlag/Default, kein fester Name — jede Installation kann hier ihre eigene, z. B. regional benannte Datenbank angeben (z. B. `stromnord`). | `jag` |
| `JAG_POSTGRES_SSLMODE` | PostgreSQL-`sslmode`-Parameter. | `disable` |

Das Schema selbst wird immer im PostgreSQL-Standardschema `public` angelegt (`CREATE TABLE
IF NOT EXISTS ...` ohne Schema-Qualifizierung) — JAG legt kein eigenes Schema an und bietet
dafür bewusst keine eigene Umgebungsvariable an; wer die Tabellen in einem anderen Schema
haben möchte, steuert das server-/rollenseitig über den `search_path` (z. B. via
`JAG_POSTGRES_DSN`'s `search_path`-Query-Parameter).

Beispiel für einen lokalen Testlauf gegen einen Docker-Container:

```
docker run -d --name jag-pg -e POSTGRES_USER=jag -e POSTGRES_PASSWORD=jag -e POSTGRES_DB=jag -p 5432:5432 postgres:16-alpine
JAG_BACKEND=postgres go run ./cmd/phase2check examples/cgmes/BaseCase_Complete
```

`cmd/hjsonimport` verwendet aktuell noch ausschließlich SQLite — das PostgreSQL-Backend ist
dort (noch) nicht verdrahtet.


**Hinweis (aktueller Implementierungsstand)**: Phase 2/3 laufen seit dem Pass-A/B-Umbau
(siehe `spec/Konzept.md`, Abschnitt "Pass A/B: Batch-weise Phase-2/3-Pipeline") nicht mehr als
einzelne whole-model-Schritte, sondern batch-weise über `common.RunPassA` (Stationen) gefolgt
von `common.RunPassB` (stationsübergreifende ACLineSegment-/Junction-Ketten) und einer
abschließenden, paged flag-basierten Vollständigkeitsprüfung (`common.CheckInvariantsFlagged`).
Frühere Umgebungsvariablen `JAG_TERMINAL_WORKERS`, `JAG_STATION_WORKERS` (alte Bedeutung:
Sachdaten+Geometrie-Worker), `JAG_DISABLE_ANHAENGSEL` und `JAG_SACHDATEN_SAMPLE` existieren im
aktuellen Code nicht mehr (die whole-model-Funktionen, die sie steuerten, werden von
`cmd/phase2check` nicht mehr aufgerufen) und wurden aus dieser Tabelle entfernt.


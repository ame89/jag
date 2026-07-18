# jag

## Umgebungsvariablen (`cmd/phase2check`)

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

**Hinweis (aktueller Implementierungsstand)**: Phase 2/3 laufen seit dem Pass-A/B-Umbau
(siehe `spec/Konzept.md`, Abschnitt "Pass A/B: Batch-weise Phase-2/3-Pipeline") nicht mehr als
einzelne whole-model-Schritte, sondern batch-weise über `common.RunPassA` (Stationen) gefolgt
von `common.RunPassB` (stationsübergreifende ACLineSegment-/Junction-Ketten) und einer
abschließenden, paged flag-basierten Vollständigkeitsprüfung (`common.CheckInvariantsFlagged`).
Frühere Umgebungsvariablen `JAG_TERMINAL_WORKERS`, `JAG_STATION_WORKERS` (alte Bedeutung:
Sachdaten+Geometrie-Worker), `JAG_DISABLE_ANHAENGSEL` und `JAG_SACHDATEN_SAMPLE` existieren im
aktuellen Code nicht mehr (die whole-model-Funktionen, die sie steuerten, werden von
`cmd/phase2check` nicht mehr aufgerufen) und wurden aus dieser Tabelle entfernt.


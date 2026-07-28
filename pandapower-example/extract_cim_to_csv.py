#!/usr/bin/env python3
"""Extract pandapower-ready CSV tables directly from the raw CGMES 2.4.15
XML profiles of the ReliCapGrid_Espheim example (EQ/SSH/TP).

This does NOT use pandapower's built-in CIM/CGMES converter
(pandapower.converter.cim). Instead it hand-parses the CIM XML with lxml,
resolves the handful of cross-references needed (Terminal -> TopologicalNode,
TopologicalNode -> VoltageLevel -> nominal voltage, PowerTransformerEnd ->
Terminal, etc.) and writes plain CSV files under data/. run_powerflow.py then
builds a pandapower net purely from those CSVs.

Why TopologicalNode (from the TP profile) instead of raw ConnectivityNode:
ReliCapGrid_Espheim is a node-breaker model (Breakers/Disconnectors between
ConnectivityNodes). The TP profile already carries the network-operator's own
switch-collapsing (closed switches merged, open switches kept apart) as
Terminal.TopologicalNode associations, so re-deriving that ourselves would
just duplicate work the source data already did correctly. Buses in the CSV
output are therefore one row per TopologicalNode.

Source data (read-only, NOT copied into this folder):
  ../examples/cgmes/ReliCapGrid_Espheim/20220615T2230Z__Espheim_EQ_1.xml
  ../examples/cgmes/ReliCapGrid_Espheim/20220615T2230Z_2D_Espheim_SSH_1.xml
  ../examples/cgmes/ReliCapGrid_Espheim/20220615T2230Z_2D_Espheim_TP_1.xml
"""
from __future__ import annotations

import csv
import os
import re
import sys
from collections import defaultdict

from lxml import etree

RDF = "{http://www.w3.org/1999/02/22-rdf-syntax-ns#}"
CIM = "{http://iec.ch/TC57/CIM100#}"

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
EXAMPLE_DIR = os.path.join(SCRIPT_DIR, "..", "examples", "cgmes", "ReliCapGrid_Espheim")
DATA_DIR = os.path.join(SCRIPT_DIR, "data")

EQ_FILE = os.path.join(EXAMPLE_DIR, "20220615T2230Z__Espheim_EQ_1.xml")
SSH_FILE = os.path.join(EXAMPLE_DIR, "20220615T2230Z_2D_Espheim_SSH_1.xml")
TP_FILE = os.path.join(EXAMPLE_DIR, "20220615T2230Z_2D_Espheim_TP_1.xml")


def rid(el):
    return el.get(RDF + "ID")


def rabout(el):
    v = el.get(RDF + "about")
    return v.lstrip("#") if v else None


def resource(child):
    v = child.get(RDF + "resource")
    return v.lstrip("#") if v else None


def local(tag):
    return etree.QName(tag).localname if isinstance(tag, str) else None


def iter_elements(root, tagname):
    want = CIM + tagname
    for e in root:
        if isinstance(e.tag, str) and e.tag == want:
            yield e


def attrs(el):
    """Return {localName: text} and {localName: resource-id} for direct children."""
    vals, refs = {}, {}
    for c in el:
        if not isinstance(c.tag, str):
            continue
        name = local(c.tag)
        r = resource(c)
        if r is not None:
            refs[name] = r
        else:
            vals[name] = c.text
    return vals, refs


def f(x, default=0.0):
    try:
        return float(x)
    except (TypeError, ValueError):
        return default


def voltage_from_name(name):
    """VoltageLevel names in this dataset encode the nominal voltage directly
    in one of a few observed patterns:
      'GON220KV'      -> 220.0   (most common: <code><kv>KV)
      'EH_SD_400kV'   -> 400.0   (boundary point naming)
      'VL_33_Needlehole' -> 33.0 (VL_<kv>_<name>)
      'VL10.5'        -> 10.5   (VL<kv>, generator terminal voltage level)
    A handful of names (e.g. plain 'EH_border3', 'TerminalES41') carry no
    voltage at all and are left unresolved here; see the PowerTransformerEnd
    fallback used for buses in extract().
    """
    if not name:
        return None
    up = name.upper()
    m = re.search(r"(\d+(?:\.\d+)?)\s*KV\s*$", up)
    if m:
        return float(m.group(1))
    m = re.search(r"^VL_?(\d+(?:\.\d+)?)(?:_|$)", up)
    if m:
        return float(m.group(1))
    return None


def main():
    if not os.path.isfile(EQ_FILE):
        sys.exit(f"EQ file not found: {EQ_FILE}\nExpected examples/cgmes/ReliCapGrid_Espheim/ next to this repo.")

    print("Parsing EQ profile ...")
    eq = etree.parse(EQ_FILE).getroot()
    print("Parsing SSH profile ...")
    ssh = etree.parse(SSH_FILE).getroot()
    print("Parsing TP profile ...")
    tp = etree.parse(TP_FILE).getroot()

    # ---- TP profile: Terminal -> TopologicalNode, TopologicalNode -> container ----
    terminal_to_tn = {}
    for e in iter_elements(tp, "Terminal"):
        _, refs = attrs(e)
        tn = refs.get("Terminal.TopologicalNode")
        about = rabout(e)
        if about and tn:
            terminal_to_tn[about] = tn

    tnodes = {}  # tn_id -> {name, container}
    for e in iter_elements(tp, "TopologicalNode"):
        vals, refs = attrs(e)
        tnodes[rid(e)] = {
            "name": vals.get("IdentifiedObject.name"),
            "container": refs.get("TopologicalNode.ConnectivityNodeContainer"),
        }

    # ---- EQ profile: VoltageLevel (container -> nominal kV via name pattern) ----
    container_kv = {}
    container_name = {}
    for e in iter_elements(eq, "VoltageLevel"):
        vals, _ = attrs(e)
        name = vals.get("IdentifiedObject.name")
        kv = voltage_from_name(name)
        container_kv[rid(e)] = kv
        container_name[rid(e)] = name

    def tn_kv(tn_id):
        node = tnodes.get(tn_id)
        if not node:
            return None
        return container_kv.get(node["container"])

    # Fallback: for TopologicalNodes whose VoltageLevel name doesn't encode a
    # voltage (boundary points, generator terminals), infer vn_kv from any
    # PowerTransformerEnd terminal known to sit on that node.
    tn_kv_fallback = {}
    for e in iter_elements(eq, "PowerTransformerEnd"):
        vals, refs = attrs(e)
        term = refs.get("TransformerEnd.Terminal")
        tn = terminal_to_tn.get(term)
        rated_u = f(vals.get("PowerTransformerEnd.ratedU"), None)
        if tn and rated_u:
            tn_kv_fallback.setdefault(tn, rated_u)

    def tn_name(tn_id):
        node = tnodes.get(tn_id)
        return node["name"] if node else tn_id

    # ---- EQ profile: Terminal -> ConductingEquipment, resolved to TopologicalNode ----
    # equipment_id -> ordered list of (sequenceNumber, terminal_id)
    equip_terminals = defaultdict(list)
    for e in iter_elements(eq, "Terminal"):
        vals, refs = attrs(e)
        eq_id = refs.get("Terminal.ConductingEquipment")
        seq = int(vals.get("ACDCTerminal.sequenceNumber", "0") or 0)
        if eq_id:
            equip_terminals[eq_id].append((seq, rid(e)))
    for lst in equip_terminals.values():
        lst.sort()

    def equip_buses(equip_id, n=2):
        """Return up to n TopologicalNode ids for an equipment's terminals, in
        ACDCTerminal.sequenceNumber order."""
        buses = []
        for _, term_id in equip_terminals.get(equip_id, [])[:n]:
            buses.append(terminal_to_tn.get(term_id))
        return buses

    # ---- EQ profile: OperationalLimitSet/CurrentLimit -> per-equipment thermal rating ----
    # CurrentLimit -> OperationalLimitSet -> Terminal -> ConductingEquipment.
    # A line has one OperationalLimitSet per terminal (both ends), each may
    # carry multiple CurrentLimits (e.g. "patl"=permanent, "tatl"=temporary
    # emergency rating). We only use "patl" (permanent admissible transmission
    # loading) as the steady-state thermal limit and take the minimum across
    # a line's terminals/limit-sets, which is the conservative (binding) value.
    limit_type_kind = {}
    for e in iter_elements(eq, "OperationalLimitType"):
        _, refs = attrs(e)
        # OperationalLimitType.kind is an rdf:resource enum reference (e.g.
        # ".../CIM100-European#LimitKind.patl"), not a literal value.
        kind = refs.get("OperationalLimitType.kind") or ""
        limit_type_kind[rid(e)] = kind.rsplit(".", 1)[-1].lower()

    ols_terminal = {}
    for e in iter_elements(eq, "OperationalLimitSet"):
        _, refs = attrs(e)
        ols_terminal[rid(e)] = refs.get("OperationalLimitSet.Terminal")

    term_to_equip = {}
    for eq_id, lst in equip_terminals.items():
        for _, term_id in lst:
            term_to_equip[term_id] = eq_id

    equip_current_limit_a = defaultdict(list)  # equip_id -> [normalValue in A, ...]
    for e in iter_elements(eq, "CurrentLimit"):
        vals, refs = attrs(e)
        limit_type = refs.get("OperationalLimit.OperationalLimitType")
        if limit_type_kind.get(limit_type) != "patl":
            continue  # skip emergency/temporary ratings, keep only the permanent one
        ols_id = refs.get("OperationalLimit.OperationalLimitSet")
        term_id = ols_terminal.get(ols_id)
        equip_id = term_to_equip.get(term_id)
        normal_value = f(vals.get("CurrentLimit.normalValue"), None)
        if equip_id and normal_value:
            equip_current_limit_a[equip_id].append(normal_value)

    def equip_max_i_ka(equip_id, default=1.0):
        vals = equip_current_limit_a.get(equip_id)
        return (min(vals) / 1000.0) if vals else default

    # Second fallback: an ACLineSegment never changes voltage level, so any
    # bus still missing vn_kv can inherit it from the other end of a line it
    # participates in. Iterate a few times since propagation may need more
    # than one hop across chains of unresolved buses.
    line_edges = []
    for e in iter_elements(eq, "ACLineSegment"):
        eid = rid(e)
        buses = equip_buses(eid)
        if len(buses) == 2 and None not in buses:
            line_edges.append((buses[0], buses[1]))

    def resolved_kv(tn_id):
        kv = tn_kv(tn_id)
        if kv is None:
            kv = tn_kv_fallback.get(tn_id)
        return kv

    line_kv_fallback = {}
    for _ in range(5):
        changed = False
        for a, b in line_edges:
            kv_a = resolved_kv(a) or line_kv_fallback.get(a)
            kv_b = resolved_kv(b) or line_kv_fallback.get(b)
            if kv_a and not kv_b and b not in line_kv_fallback:
                line_kv_fallback[b] = kv_a
                changed = True
            if kv_b and not kv_a and a not in line_kv_fallback:
                line_kv_fallback[a] = kv_b
                changed = True
        if not changed:
            break

    # ---- SSH profile: per-equipment operating values, keyed by rdf:about ----
    ssh_vals = defaultdict(dict)
    for e in ssh:
        if not isinstance(e.tag, str):
            continue
        about = rabout(e)
        if not about:
            continue
        vals, refs = attrs(e)
        ssh_vals[about].update(vals)

    def bus_vn_kv(tn_id):
        kv = tn_kv(tn_id)
        if kv is None:
            kv = tn_kv_fallback.get(tn_id)
        if kv is None:
            kv = line_kv_fallback.get(tn_id)
        return kv

    # ---- Build in-memory row lists for every table before filtering ----
    line_rows = []
    for e in iter_elements(eq, "ACLineSegment"):
        vals, _ = attrs(e)
        eid = rid(e)
        buses = equip_buses(eid)
        if len(buses) < 2 or None in buses:
            continue
        length = f(vals.get("Conductor.length"), 1.0) or 1.0
        r = f(vals.get("ACLineSegment.r"))
        x = f(vals.get("ACLineSegment.x"))
        bch = f(vals.get("ACLineSegment.bch"))  # total shunt susceptance, siemens
        gch = f(vals.get("ACLineSegment.gch"))
        # ACLineSegment.r/x/bch/gch are TOTAL values for the segment (not per
        # km) -> convert to per-km for pandapower's create_line_from_parameters.
        c_nf = bch / (2 * 3.14159265358979 * 50.0) * 1e9  # susceptance -> capacitance (50 Hz)
        in_service = ssh_vals.get(eid, {}).get("Equipment.inService", "true") == "true"
        line_rows.append({
            "line_id": eid, "name": vals.get("IdentifiedObject.name"),
            "from_bus": buses[0], "to_bus": buses[1], "length_km": length,
            "r_ohm_per_km": r / length, "x_ohm_per_km": x / length,
            "c_nf_per_km": c_nf / length, "g_us_per_km": (gch / length) * 1e6,
            "max_i_ka": equip_max_i_ka(eid),
            "in_service": in_service,
        })

    trafo_ends = defaultdict(list)
    trafo_name = {}
    for e in iter_elements(eq, "PowerTransformerEnd"):
        vals, refs = attrs(e)
        pt_id = refs.get("PowerTransformerEnd.PowerTransformer")
        trafo_ends[pt_id].append({
            "end_number": int(vals.get("TransformerEnd.endNumber", "0") or 0),
            "terminal": refs.get("TransformerEnd.Terminal"),
            "r": f(vals.get("PowerTransformerEnd.r")),
            "x": f(vals.get("PowerTransformerEnd.x")),
            "ratedS": f(vals.get("PowerTransformerEnd.ratedS")),
            "ratedU": f(vals.get("PowerTransformerEnd.ratedU")),
        })
    for e in iter_elements(eq, "PowerTransformer"):
        vals, _ = attrs(e)
        trafo_name[rid(e)] = vals.get("IdentifiedObject.name")

    trafo_rows = []
    for pt_id, ends in trafo_ends.items():
        if len(ends) != 2:
            continue
        ends = sorted(ends, key=lambda d: d["end_number"])
        hv, lv = ends[0], ends[1]
        hv_bus = terminal_to_tn.get(hv["terminal"])
        lv_bus = terminal_to_tn.get(lv["terminal"])
        if not hv_bus or not lv_bus:
            continue
        sn_mva = hv["ratedS"] or lv["ratedS"] or 1.0
        vn_hv = hv["ratedU"]
        vn_lv = lv["ratedU"]
        # Each end's r/x is given in ohms referred to its own rated voltage;
        # refer the LV end's impedance to the HV side (standard transformer
        # equivalent-circuit convention) before summing into one short-circuit
        # impedance for pandapower's two-winding model.
        r_total = hv["r"] + lv["r"] * (vn_hv / vn_lv) ** 2 if vn_lv else hv["r"]
        x_total = hv["x"] + lv["x"] * (vn_hv / vn_lv) ** 2 if vn_lv else hv["x"]
        z_base = (vn_hv ** 2) / sn_mva if sn_mva else 1.0
        vk_percent = (((r_total ** 2 + x_total ** 2) ** 0.5) / z_base) * 100.0 if z_base else 0.0
        vkr_percent = (r_total / z_base) * 100.0 if z_base else 0.0
        in_service = ssh_vals.get(pt_id, {}).get("Equipment.inService", "true") == "true"
        trafo_rows.append({
            "trafo_id": pt_id, "name": trafo_name.get(pt_id), "hv_bus": hv_bus, "lv_bus": lv_bus,
            "sn_mva": sn_mva, "vn_hv_kv": vn_hv, "vn_lv_kv": vn_lv,
            "vk_percent": round(vk_percent, 6), "vkr_percent": round(vkr_percent, 6),
            "in_service": in_service,
        })

    load_rows = []
    for e in iter_elements(eq, "ConformLoad"):
        vals, _ = attrs(e)
        eid = rid(e)
        buses = equip_buses(eid, n=1)
        if not buses or buses[0] is None:
            continue
        sv = ssh_vals.get(eid, {})
        load_rows.append({
            "load_id": eid, "name": vals.get("IdentifiedObject.name"), "bus": buses[0],
            "p_mw": f(sv.get("EnergyConsumer.p")), "q_mvar": f(sv.get("EnergyConsumer.q")),
            "in_service": sv.get("Equipment.inService", "true") == "true",
        })

    gen_rows = []
    for e in iter_elements(eq, "SynchronousMachine"):
        vals, refs = attrs(e)
        eid = rid(e)
        buses = equip_buses(eid, n=1)
        if not buses or buses[0] is None:
            continue
        sv = ssh_vals.get(eid, {})
        # RotatingMachine.p: CGMES load-sign convention is generation =
        # negative; pandapower's gen convention wants generated active power
        # as a positive value, so flip sign here. Modeled as a voltage-
        # controlled (PV) generator -- not a fixed-Q sgen -- since these are
        # real synchronous machines with automatic voltage regulation
        # (RegulatingControl.mode = voltage in the source data); reactive
        # power is left for the power flow to solve within [minQ, maxQ].
        gen_rows.append({
            "gen_id": eid, "name": vals.get("IdentifiedObject.name"), "bus": buses[0],
            "p_mw": -f(sv.get("RotatingMachine.p")),
            "vm_pu": 1.0,
            "min_q_mvar": f(vals.get("SynchronousMachine.minQ"), -9999.0),
            "max_q_mvar": f(vals.get("SynchronousMachine.maxQ"), 9999.0),
            "in_service": sv.get("Equipment.inService", "true") == "true",
        })

    ext_grid_rows = []
    for e in iter_elements(eq, "ExternalNetworkInjection"):
        vals, _ = attrs(e)
        eid = rid(e)
        buses = equip_buses(eid, n=1)
        if not buses or buses[0] is None:
            continue
        sv = ssh_vals.get(eid, {})
        ext_grid_rows.append({
            "ext_grid_id": eid, "name": vals.get("IdentifiedObject.name"), "bus": buses[0],
            "vm_pu": 1.0, "in_service": sv.get("Equipment.inService", "true") == "true",
        })

    if not ext_grid_rows:
        print("WARNING: no ExternalNetworkInjection found -> run_powerflow.py will need a fallback slack bus.")

    # ---- Connectivity filter ----
    # ReliCapGrid_Espheim's TP profile contains a small number of buses (~45 of
    # 177) that end up with no ACLineSegment/PowerTransformer edge at all --
    # single-bus islands with no path to either ExternalNetworkInjection. A
    # power flow has no way to supply these (no slack reachable), so pandapower
    # fails to converge if they're included in-service. We keep only the buses
    # reachable from an ext_grid bus and drop equipment attached to the rest,
    # documented here and in README.md rather than silently guessing values.
    adjacency = defaultdict(set)
    for r in line_rows:
        adjacency[r["from_bus"]].add(r["to_bus"])
        adjacency[r["to_bus"]].add(r["from_bus"])
    for r in trafo_rows:
        adjacency[r["hv_bus"]].add(r["lv_bus"])
        adjacency[r["lv_bus"]].add(r["hv_bus"])

    reachable = set()
    frontier = [r["bus"] for r in ext_grid_rows]
    reachable.update(frontier)
    while frontier:
        nxt = []
        for b in frontier:
            for n in adjacency.get(b, ()):
                if n not in reachable:
                    reachable.add(n)
                    nxt.append(n)
        frontier = nxt

    if not reachable:
        # No ext_grid at all (shouldn't happen for this dataset) -> keep the
        # single largest connected component instead of nothing.
        seen, best = set(), set()
        for start in tnodes:
            if start in seen:
                continue
            comp, frontier = {start}, [start]
            while frontier:
                nxt = []
                for b in frontier:
                    for n in adjacency.get(b, ()):
                        if n not in comp:
                            comp.add(n)
                            nxt.append(n)
                frontier = nxt
            seen |= comp
            if len(comp) > len(best):
                best = comp
        reachable = best

    n_all_buses = len(tnodes)
    dropped_buses = n_all_buses - len(reachable)
    line_rows = [r for r in line_rows if r["from_bus"] in reachable and r["to_bus"] in reachable]
    trafo_rows = [r for r in trafo_rows if r["hv_bus"] in reachable and r["lv_bus"] in reachable]
    load_rows = [r for r in load_rows if r["bus"] in reachable]
    gen_rows = [r for r in gen_rows if r["bus"] in reachable]
    ext_grid_rows = [r for r in ext_grid_rows if r["bus"] in reachable]

    os.makedirs(DATA_DIR, exist_ok=True)

    # ================= buses.csv =================
    bus_ids = sorted(reachable)
    with open(os.path.join(DATA_DIR, "buses.csv"), "w", newline="", encoding="utf-8") as fh:
        w = csv.writer(fh)
        w.writerow(["bus_id", "name", "vn_kv"])
        n_missing_kv = 0
        for tn_id in bus_ids:
            kv = bus_vn_kv(tn_id)
            if kv is None:
                n_missing_kv += 1
                kv = 0.0
            w.writerow([tn_id, tnodes[tn_id]["name"], kv])
    print(f"buses.csv: {len(bus_ids)} rows ({n_missing_kv} without resolvable vn_kv; "
          f"{dropped_buses} of {n_all_buses} total buses dropped as unreachable from any ext_grid)")

    # ================= lines.csv =================
    with open(os.path.join(DATA_DIR, "lines.csv"), "w", newline="", encoding="utf-8") as fh:
        w = csv.writer(fh)
        cols = ["line_id", "name", "from_bus", "to_bus", "length_km", "r_ohm_per_km", "x_ohm_per_km", "c_nf_per_km", "g_us_per_km", "max_i_ka", "in_service"]
        w.writerow(cols)
        for r in line_rows:
            w.writerow([r[c] for c in cols])
    print(f"lines.csv: {len(line_rows)} rows")

    # ================= trafos.csv =================
    with open(os.path.join(DATA_DIR, "trafos.csv"), "w", newline="", encoding="utf-8") as fh:
        w = csv.writer(fh)
        cols = ["trafo_id", "name", "hv_bus", "lv_bus", "sn_mva", "vn_hv_kv", "vn_lv_kv", "vk_percent", "vkr_percent", "in_service"]
        w.writerow(cols)
        for r in trafo_rows:
            w.writerow([r[c] for c in cols])
    print(f"trafos.csv: {len(trafo_rows)} rows")

    # ================= loads.csv =================
    with open(os.path.join(DATA_DIR, "loads.csv"), "w", newline="", encoding="utf-8") as fh:
        w = csv.writer(fh)
        cols = ["load_id", "name", "bus", "p_mw", "q_mvar", "in_service"]
        w.writerow(cols)
        for r in load_rows:
            w.writerow([r[c] for c in cols])
    print(f"loads.csv: {len(load_rows)} rows")

    # ================= gens.csv (SynchronousMachine, PV/voltage-controlled) =================
    with open(os.path.join(DATA_DIR, "gens.csv"), "w", newline="", encoding="utf-8") as fh:
        w = csv.writer(fh)
        cols = ["gen_id", "name", "bus", "p_mw", "vm_pu", "min_q_mvar", "max_q_mvar", "in_service"]
        w.writerow(cols)
        for r in gen_rows:
            w.writerow([r[c] for c in cols])
    print(f"gens.csv: {len(gen_rows)} rows")

    # ================= ext_grid.csv =================
    with open(os.path.join(DATA_DIR, "ext_grid.csv"), "w", newline="", encoding="utf-8") as fh:
        w = csv.writer(fh)
        cols = ["ext_grid_id", "name", "bus", "vm_pu", "in_service"]
        w.writerow(cols)
        for r in ext_grid_rows:
            w.writerow([r[c] for c in cols])
    print(f"ext_grid.csv: {len(ext_grid_rows)} rows")

    print(f"\nAll CSVs written to {DATA_DIR}")


if __name__ == "__main__":
    main()

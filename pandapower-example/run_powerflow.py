#!/usr/bin/env python3
"""Build a pandapower network purely from the CSV files in ./data (produced
by extract_cim_to_csv.py) and run an AC power flow, printing results to the
console.

Usage:
    python run_powerflow.py                # uses ./data
    python run_powerflow.py /path/to/data  # custom data dir (e.g. in Docker)
"""
from __future__ import annotations

import os
import sys

import pandas as pd
import pandapower as pp


def load_csv(data_dir, name):
    path = os.path.join(data_dir, name)
    if not os.path.isfile(path):
        sys.exit(f"Missing {path} -- run extract_cim_to_csv.py first.")
    return pd.read_csv(path, dtype={"in_service": str})


def to_bool(series):
    return series.astype(str).str.strip().str.lower().isin(["true", "1", "yes"])


def build_net(data_dir):
    buses = load_csv(data_dir, "buses.csv")
    lines = load_csv(data_dir, "lines.csv")
    trafos = load_csv(data_dir, "trafos.csv")
    loads = load_csv(data_dir, "loads.csv")
    gens = load_csv(data_dir, "gens.csv")
    ext_grid = load_csv(data_dir, "ext_grid.csv")

    net = pp.create_empty_network(name="ReliCapGrid_Espheim")

    bus_idx = {}
    for _, r in buses.iterrows():
        vn_kv = float(r["vn_kv"]) or 0.4  # guard against stray 0 kV buses
        idx = pp.create_bus(net, vn_kv=vn_kv, name=str(r["name"]) if pd.notna(r["name"]) else r["bus_id"])
        bus_idx[r["bus_id"]] = idx

    n_lines = 0
    for _, r in lines.iterrows():
        if r["from_bus"] not in bus_idx or r["to_bus"] not in bus_idx:
            continue
        pp.create_line_from_parameters(
            net,
            from_bus=bus_idx[r["from_bus"]],
            to_bus=bus_idx[r["to_bus"]],
            length_km=float(r["length_km"]),
            r_ohm_per_km=float(r["r_ohm_per_km"]),
            x_ohm_per_km=float(r["x_ohm_per_km"]),
            c_nf_per_km=max(float(r["c_nf_per_km"]), 0.0),
            g_us_per_km=max(float(r.get("g_us_per_km", 0.0)), 0.0),
            max_i_ka=float(r["max_i_ka"]),  # from CIM CurrentLimit.normalValue (patl), converted A->kA
            name=str(r["name"]) if pd.notna(r["name"]) else r["line_id"],
            in_service=bool(to_bool(pd.Series([r["in_service"]]))[0]),
        )
        n_lines += 1

    n_trafo = 0
    for _, r in trafos.iterrows():
        if r["hv_bus"] not in bus_idx or r["lv_bus"] not in bus_idx:
            continue
        pp.create_transformer_from_parameters(
            net,
            hv_bus=bus_idx[r["hv_bus"]],
            lv_bus=bus_idx[r["lv_bus"]],
            sn_mva=float(r["sn_mva"]),
            vn_hv_kv=float(r["vn_hv_kv"]),
            vn_lv_kv=float(r["vn_lv_kv"]),
            vk_percent=max(float(r["vk_percent"]), 0.01),
            vkr_percent=float(r["vkr_percent"]),
            pfe_kw=0.0,
            i0_percent=0.0,
            name=str(r["name"]) if pd.notna(r["name"]) else r["trafo_id"],
            in_service=bool(to_bool(pd.Series([r["in_service"]]))[0]),
        )
        n_trafo += 1

    n_loads = 0
    for _, r in loads.iterrows():
        if r["bus"] not in bus_idx:
            continue
        pp.create_load(
            net,
            bus=bus_idx[r["bus"]],
            p_mw=float(r["p_mw"]),
            q_mvar=float(r["q_mvar"]),
            name=str(r["name"]) if pd.notna(r["name"]) else r["load_id"],
            in_service=bool(to_bool(pd.Series([r["in_service"]]))[0]),
        )
        n_loads += 1

    n_gen = 0
    for _, r in gens.iterrows():
        if r["bus"] not in bus_idx:
            continue
        pp.create_gen(
            net,
            bus=bus_idx[r["bus"]],
            p_mw=float(r["p_mw"]),
            vm_pu=float(r["vm_pu"]),
            min_q_mvar=float(r["min_q_mvar"]),
            max_q_mvar=float(r["max_q_mvar"]),
            name=str(r["name"]) if pd.notna(r["name"]) else r["gen_id"],
            in_service=bool(to_bool(pd.Series([r["in_service"]]))[0]),
        )
        n_gen += 1

    n_ext = 0
    for _, r in ext_grid.iterrows():
        if r["bus"] not in bus_idx:
            continue
        pp.create_ext_grid(
            net,
            bus=bus_idx[r["bus"]],
            vm_pu=float(r["vm_pu"]),
            name=str(r["name"]) if pd.notna(r["name"]) else r["ext_grid_id"],
            in_service=bool(to_bool(pd.Series([r["in_service"]]))[0]),
        )
        n_ext += 1

    if n_ext == 0:
        # No ExternalNetworkInjection resolved to a bus -- power flow needs at
        # least one slack. Fall back to the highest-voltage bus.
        slack_bus = buses.loc[buses["vn_kv"].idxmax(), "bus_id"]
        print(f"No ext_grid rows resolved -- using highest-voltage bus {slack_bus} as fallback slack.")
        pp.create_ext_grid(net, bus=bus_idx[slack_bus], vm_pu=1.0, name="fallback_slack")
        n_ext = 1

    print(f"Network built: {len(bus_idx)} buses, {n_lines} lines, {n_trafo} trafos, "
          f"{n_loads} loads, {n_gen} gens, {n_ext} ext_grid(s)")
    return net


def main():
    data_dir = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "data")
    net = build_net(data_dir)

    print("\nRunning AC power flow (pp.runpp) ...")
    try:
        pp.runpp(net, algorithm="nr", calculate_voltage_angles=True)
    except Exception as exc:  # pandapower raises LoadflowNotConverged etc.
        print(f"\nPower flow did NOT converge: {exc}")
        sys.exit(1)

    pd.set_option("display.max_rows", 300)
    pd.set_option("display.width", 160)

    print("\n=== Bus results (res_bus) ===")
    print(net.res_bus.join(net.bus[["name", "vn_kv"]]).sort_index())

    print("\n=== Line loading (res_line) ===")
    print(net.res_line.join(net.line[["name"]]).sort_values("loading_percent", ascending=False))

    print("\n=== Transformer loading (res_trafo) ===")
    print(net.res_trafo.join(net.trafo[["name"]]))

    print("\n=== Generator results (res_gen) ===")
    print(net.res_gen.join(net.gen[["name", "bus"]]))

    print("\n=== External grid feed-in (res_ext_grid) ===")
    print(net.res_ext_grid.join(net.ext_grid[["name", "bus"]]))

    print("\n=== Summary ===")
    print(f"Total load:  P = {net.res_load['p_mw'].sum():.2f} MW, Q = {net.res_load['q_mvar'].sum():.2f} MVAr")
    print(f"Total gen:   P = {net.res_gen['p_mw'].sum():.2f} MW, Q = {net.res_gen['q_mvar'].sum():.2f} MVAr")
    print(f"Ext. grid:   P = {net.res_ext_grid['p_mw'].sum():.2f} MW, Q = {net.res_ext_grid['q_mvar'].sum():.2f} MVAr")
    print(f"Min/max bus voltage: {net.res_bus['vm_pu'].min():.4f} / {net.res_bus['vm_pu'].max():.4f} p.u.")
    print(f"Max line loading: {net.res_line['loading_percent'].max():.1f} %")


if __name__ == "__main__":
    main()

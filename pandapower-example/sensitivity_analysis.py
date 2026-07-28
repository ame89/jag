#!/usr/bin/env python3
"""Sensitivity analysis for the ReliCapGrid_Espheim network: PTDF and LODF.

- PTDF (Power Transfer Distribution Factors): how much a branch's active-power
  flow changes per MW of injection change at a given bus, relative to the
  slack. Answers "if I add/remove load or generation at bus X, how much does
  the flow on line Y change?".
- LODF (Line Outage Distribution Factors): how much flow shifts onto other
  branches if a given branch is taken out of service (n-1 contingency
  screening). Answers "if line X trips, how much extra flow lands on line Y?".

Both are computed from pandapower's internal ppc arrays via
pandapower.pypower.makePTDF / makeLODF (the same PYPOWER routines pandapower
itself is built on for DC-approximation sensitivity analysis) -- not
re-derived by hand.

Also prints the shape of the AC Newton-Raphson Jacobian (net._ppc["internal"]["J"])
used for the last power flow, for reference (see README.md section "Jacobian").

Usage:
    python sensitivity_analysis.py [data_dir]
"""
from __future__ import annotations

import os
import sys

import numpy as np
import pandas as pd

import pandapower as pp
from pandapower.pypower.makePTDF import makePTDF
from pandapower.pypower.makeLODF import makeLODF
from pandapower.pypower.idx_brch import F_BUS, T_BUS

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from run_powerflow import build_net  # noqa: E402

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
DOCS_DIR = os.path.join(SCRIPT_DIR, "docs")


def ppc_bus_to_pd_name(net, ppc):
    """Map ppc-internal bus row index -> pandapower bus name (best-effort;
    if several pandapower buses were merged onto the same ppc bus, the first
    one found is used)."""
    bus_lookup = net._pd2ppc_lookups["bus"]
    ppc_to_pd = {}
    for pd_idx, ppc_idx in enumerate(bus_lookup):
        if ppc_idx >= 0:
            ppc_to_pd.setdefault(ppc_idx, pd_idx)
    names = {}
    for ppc_idx, pd_idx in ppc_to_pd.items():
        names[ppc_idx] = net.bus.at[pd_idx, "name"]
    return names, ppc_to_pd


def ppc_branch_to_pd_name(net, ppc, ppc_to_pd):
    """Map ppc-internal branch row index -> ('line'|'trafo', pandapower name),
    by matching (from_bus, to_bus) ppc indices back to net.line/net.trafo
    (undirected, since PTDF/LODF orientation is fixed by ppc branch order)."""
    branch = ppc["branch"]
    line_ends = {}
    for idx, row in net.line.iterrows():
        line_ends[frozenset((row["from_bus"], row["to_bus"]))] = ("line", row["name"], idx)
    trafo_ends = {}
    for idx, row in net.trafo.iterrows():
        trafo_ends[frozenset((row["hv_bus"], row["lv_bus"]))] = ("trafo", row["name"], idx)

    names = []
    for r in range(branch.shape[0]):
        f_ppc = int(branch[r, F_BUS].real)
        t_ppc = int(branch[r, T_BUS].real)
        f_pd = ppc_to_pd.get(f_ppc)
        t_pd = ppc_to_pd.get(t_ppc)
        key = frozenset((f_pd, t_pd)) if f_pd is not None and t_pd is not None else None
        if key in line_ends:
            names.append(line_ends[key])
        elif key in trafo_ends:
            names.append(trafo_ends[key])
        else:
            names.append(("unknown", f"branch_{r}", r))
    return names


def main():
    data_dir = sys.argv[1] if len(sys.argv) > 1 else os.path.join(SCRIPT_DIR, "data")
    net = build_net(data_dir)
    pp.runpp(net, algorithm="nr", calculate_voltage_angles=True)

    ppc = net._ppc["internal"]
    baseMVA, bus, branch = ppc["baseMVA"], ppc["bus"], ppc["branch"]

    print(f"AC Jacobian (last Newton-Raphson iteration): shape {ppc['J'].shape} "
          f"(reference only, see README.md)")

    bus_names, ppc_to_pd = ppc_bus_to_pd_name(net, ppc)
    branch_names = ppc_branch_to_pd_name(net, ppc, ppc_to_pd)

    print(f"\nComputing PTDF ({branch.shape[0]} branches x {bus.shape[0]} buses) ...")
    ptdf = makePTDF(baseMVA, bus, branch)

    print(f"Computing LODF ({branch.shape[0]} x {branch.shape[0]}) ...")
    lodf = makeLODF(branch, ptdf)

    os.makedirs(DOCS_DIR, exist_ok=True)

    # ---- PTDF: full matrix as CSV (rows=branches, cols=buses) ----
    branch_labels = [f"{kind}:{name}" for kind, name, _ in branch_names]
    bus_labels = [bus_names.get(i, f"ppc_bus_{i}") for i in range(bus.shape[0])]
    ptdf_df = pd.DataFrame(ptdf, index=branch_labels, columns=bus_labels)
    ptdf_path = os.path.join(DOCS_DIR, "ptdf.csv")
    ptdf_df.to_csv(ptdf_path)
    print(f"Full PTDF matrix written to {ptdf_path}")

    # ---- PTDF: top-20 most sensitive (branch, bus) pairs by |PTDF| ----
    stacked = ptdf_df.stack()
    order = np.argsort(-np.abs(stacked.values))[:20]
    top_ptdf = [(stacked.index[i], stacked.values[i]) for i in order]
    print("\n=== Top 20 most sensitive (branch, bus-injection) pairs -- |PTDF| ===")
    print("(how much branch active-power flow [p.u. of baseMVA] shifts per 1 MW "
          "injection change at that bus, relative to the slack)")
    for (branch_lbl, bus_lbl), val in top_ptdf:
        print(f"  {branch_lbl:40s}  <-  bus {bus_lbl:24s}  PTDF = {val:+.4f}")

    # ---- LODF: full matrix as CSV ----
    lodf_df = pd.DataFrame(lodf, index=branch_labels, columns=branch_labels)
    lodf_path = os.path.join(DOCS_DIR, "lodf.csv")
    lodf_df.to_csv(lodf_path)
    print(f"\nFull LODF matrix written to {lodf_path}")

    # ---- LODF: top-20 largest outage -> redistribution pairs (excluding self) ----
    # Convention (see pandapower.pypower.makeLODF): LODF[row, col] = flow
    # change on the ROW (monitored) branch when the COLUMN (outaged) branch
    # is taken out of service. The denominator only depends on the outaged
    # branch's own PTDF, so +-inf entries occur in whole COLUMNS, not rows.
    lodf_no_diag = lodf_df.copy()
    np.fill_diagonal(lodf_no_diag.values, np.nan)
    finite_mask = np.isfinite(lodf_no_diag.values)

    # Branches whose outage produces +-inf LODF for other branches are
    # structural "bridges": removing them would island part of the network
    # (no alternative path exists at all) -- a genuinely interesting n-1
    # finding, not a numerical artifact, so report it separately instead of
    # drowning the top-20 list in infinities.
    is_bridge_col = np.zeros(lodf_no_diag.shape[1], dtype=bool)
    for j in range(lodf_no_diag.shape[1]):
        col = np.delete(lodf_no_diag.values[:, j], j)  # exclude the NaN diagonal itself
        is_bridge_col[j] = not np.isfinite(col).all()
    bridge_branches = sorted({branch_labels[j] for j in np.where(is_bridge_col)[0]})
    print(f"\n=== Structural bridges ({len(bridge_branches)} of {len(branch_labels)} branches) ===")
    print("Outage of any of these islands part of the network (no alternative path -> "
          "LODF undefined/infinite for every other branch). These are single "
          "points of failure with zero (n-1) redundancy:")
    for b in bridge_branches[:30]:
        print(f"  {b}")
    if len(bridge_branches) > 30:
        print(f"  ... and {len(bridge_branches) - 30} more (see docs/lodf.csv)")

    finite_vals = np.where(finite_mask, lodf_no_diag.values, np.nan)
    order_lodf = np.argsort(-np.abs(np.nan_to_num(finite_vals, nan=-1)).ravel())
    flat_finite = finite_vals.ravel()
    top_lodf = []
    for idx in order_lodf:
        if np.isnan(flat_finite[idx]):
            continue
        r, c = divmod(idx, finite_vals.shape[1])
        top_lodf.append(((branch_labels[c], branch_labels[r]), flat_finite[idx]))
        if len(top_lodf) == 20:
            break
    print("\n=== Top 20 finite n-1 redistribution pairs -- |LODF| ===")
    print("(fraction of the outaged branch's pre-outage flow that reappears on the other branch)")
    for (outaged, monitored), val in top_lodf:
        print(f"  outage {outaged:35s}  ->  {monitored:35s}  LODF = {val:+.4f}")

    # ---- Focus: the overloaded line (max loading_percent from run_powerflow) ----
    overloaded_line = net.res_line["loading_percent"].idxmax()
    overloaded_name = net.line.at[overloaded_line, "name"]
    overloaded_loading = net.res_line.at[overloaded_line, "loading_percent"]
    overloaded_label = f"line:{overloaded_name}"
    print(f"\n=== Focus: overloaded line '{overloaded_name}' "
          f"(loading = {overloaded_loading:.1f} %) ===")
    if overloaded_label in ptdf_df.index:
        row = ptdf_df.loc[overloaded_label].abs().sort_values(ascending=False)
        print("Top 10 buses whose injection change most affects this line's flow (|PTDF|):")
        for bus_lbl, val in row.head(10).items():
            signed = ptdf_df.loc[overloaded_label, bus_lbl]
            print(f"  bus {bus_lbl:24s}  PTDF = {signed:+.4f}")
    else:
        print(f"  (label {overloaded_label!r} not found in PTDF index -- check name mapping)")

    if overloaded_label in lodf_df.columns:
        col = lodf_df[overloaded_label].drop(index=overloaded_label, errors="ignore")
        col_finite = col[np.isfinite(col.values)]
        print(f"\nIf '{overloaded_name}' itself trips, top 10 lines absorbing the most "
              f"redistributed flow (|LODF| in column '{overloaded_label}'):")
        for branch_lbl, val in col_finite.abs().sort_values(ascending=False).head(10).items():
            signed = col_finite[branch_lbl]
            print(f"  {branch_lbl:35s}  LODF = {signed:+.4f}")
    if overloaded_label in lodf_df.index:
        row2 = lodf_df.loc[overloaded_label].drop(index=overloaded_label, errors="ignore")
        row2_finite = row2[np.isfinite(row2.values)]
        print(f"\nWhich other line outages would push MOST additional flow onto the "
              f"already-overloaded '{overloaded_name}' (|LODF| in row '{overloaded_label}'):")
        for branch_lbl, val in row2_finite.abs().sort_values(ascending=False).head(10).items():
            signed = row2_finite[branch_lbl]
            print(f"  outage {branch_lbl:35s}  ->  LODF = {signed:+.4f}")

    print(f"\nDone. See {DOCS_DIR} for the full PTDF/LODF CSVs.")


if __name__ == "__main__":
    main()

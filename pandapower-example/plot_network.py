#!/usr/bin/env python3
"""Generate a plot of the ReliCapGrid_Espheim network (topology + voltage
result colouring) and save it as docs/network_diagram.png.

No geographic coordinates are extracted from the CIM data (see README.md),
so this uses pandapower's automatic generic-coordinate layout (spring-layout
style, via networkx) purely for a readable schematic -- bus positions do NOT
correspond to real-world geography.
"""
from __future__ import annotations

import os
import sys

import matplotlib
matplotlib.use("Agg")  # headless / Docker-safe backend
import matplotlib.pyplot as plt
import pandapower as pp
import pandapower.plotting as pplt

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from run_powerflow import build_net  # noqa: E402

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
DOCS_DIR = os.path.join(SCRIPT_DIR, "docs")


def main():
    data_dir = sys.argv[1] if len(sys.argv) > 1 else os.path.join(SCRIPT_DIR, "data")
    net = build_net(data_dir)

    try:
        pp.runpp(net, algorithm="nr", calculate_voltage_angles=True)
        converged = True
    except Exception as exc:
        print(f"Power flow did not converge ({exc}) -- plotting topology only, without voltage colouring.")
        converged = False

    pplt.create_generic_coordinates(net, respect_switches=False)

    os.makedirs(DOCS_DIR, exist_ok=True)

    fig, ax = plt.subplots(figsize=(16, 12))

    bus_color = net.res_bus["vm_pu"] if converged else "#1f78b4"
    collections = []
    collections.append(pplt.create_line_collection(net, net.line.index, color="grey", linewidths=1.0))
    if len(net.trafo):
        collections.append(pplt.create_trafo_collection(net, net.trafo.index, color="black", linewidths=1.5))
    if converged:
        bc = pplt.create_bus_collection(net, net.bus.index, size=0.06, cmap="RdYlGn",
                                          norm=matplotlib.colors.Normalize(vmin=0.9, vmax=1.1),
                                          z=net.res_bus["vm_pu"], cbar_title="Bus voltage [p.u.]")
    else:
        bc = pplt.create_bus_collection(net, net.bus.index, size=0.06, color="#1f78b4")
    collections.append(bc)
    if len(net.ext_grid):
        collections.append(pplt.create_bus_collection(net, net.ext_grid.bus.values, size=0.12,
                                                        color="red", patch_type="rect"))
    if len(net.gen):
        collections.append(pplt.create_bus_collection(net, net.gen.bus.values, size=0.09,
                                                        color="green", patch_type="poly3"))

    pplt.draw_collections(collections, ax=ax)
    ax.set_title(
        "ReliCapGrid_Espheim -- topology (generic layout, no real geo-coordinates)\n"
        "red square = ext_grid (slack), green triangle = synchronous generator, "
        "colour = bus voltage [p.u.]" if converged else
        "ReliCapGrid_Espheim -- topology (generic layout, no real geo-coordinates)\n"
        "red square = ext_grid (slack), green triangle = synchronous generator"
    )
    ax.set_aspect("equal")
    out_path = os.path.join(DOCS_DIR, "network_diagram.png")
    fig.savefig(out_path, dpi=150, bbox_inches="tight")
    print(f"Saved {out_path}")


if __name__ == "__main__":
    main()

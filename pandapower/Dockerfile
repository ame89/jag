# Runs the ReliCapGrid_Espheim power flow with pandapower.
#
# By default this only needs the pre-generated CSVs under ./data (checked
# into git, derived/small) -- it does NOT need the raw CIM/CGMES example data
# to run the power flow. If you want to re-run the extraction itself inside
# the container, mount the repo's examples/ folder at /app/examples and run
# extract_cim_to_csv.py before run_powerflow.py (see README.md).
FROM python:3.11-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY extract_cim_to_csv.py run_powerflow.py plot_network.py ./
COPY data ./data

CMD ["python", "run_powerflow.py"]

# NeMo Data Designer on CloudlessOS

This workspace generates and validates synthetic datasets. CloudlessOS disables
NeMo telemetry by default and keeps notebooks, inputs, and outputs in persistent
local volumes.

Open `getting-started.ipynb` for a small offline sampler example. To use the
currently loaded Cloudless model, configure a Data Designer OpenAI-compatible
provider with `http://cloudless-ai:8000/v1` and the model name `cloudless`.

# Hardware — Cloudless PC reference builds

> Phase 2 is "hardware last" ([[ROADMAP]]), but the spec work starts now so the
> software is built against real target machines. This file is the canonical
> component list + pricing rationale for the first Cloudless PCs.
>
> **All prices are mid-2026 US street prices and are volatile** — GPUs are 50%+
> over MSRP, and DDR5/NAND are mid-shortage-spike (AI/datacenter demand). Re-quote
> before any build run. Dates absolute per the working agreement.
> Last priced: **2026-06-27**.

## The design constraint that drives everything

Cloudless PCs ship **multiple GPUs to scale a *single* active inference engine via
tensor parallelism** — bigger models / higher throughput, never multiple engines at
once ([[VISION]], [[DECISIONS]] D15). Tensor parallelism (TP) exchanges activations
on **every layer** (all-reduce), so **inter-GPU bandwidth sits on the critical path.**
That single fact reorders the whole parts list:

- **Total VRAM** sets which models the box can serve.
- **GPU-to-GPU interconnect** sets how well TP scales across the cards.
- Everything else (CPU, PCIe width, RAM, PSU) serves those two.

### The NVLink cliff (most important hardware fact in 2026)

NVIDIA **removed NVLink from every consumer card except the RTX 3090.** The 4090,
5090, and all Blackwell GeForce cards are **PCIe-only**:

| Interconnect | Bandwidth | Cards |
|---|---|---|
| NVLink (Ampere) | ~112 GB/s | **RTX 3090 only** |
| PCIe 5.0 x16 | ~64 GB/s | 5090/4090 at full width |
| PCIe 5.0 x8 | ~32 GB/s | 5090/4090 on a bifurcated consumer board |

So for TP specifically, **2× RTX 3090 has ~3.5× the inter-GPU bandwidth of a 2× 5090
PCIe pair** — the cheapest viable multi-GPU config is also the most architecturally
correct one for our engine model. This is the central reason the entry SKU is 3090-based.

## The $5k math (why 2× 5090 is not a $5k machine)

A $5,000 **sell** price at a boutique ~20–30% gross margin needs a **~$3,500–4,000
BOM**. At mid-2026 prices, here's where the candidates land:

| Config | VRAM | GPU pair | Interconnect | Build BOM | Sell @ ~25% |
|---|---|---|---|---|---|
| **2× RTX 3090** | 48 GB | ~$1,600 (used) | **NVLink** | **~$3,850** | **~$5,100** |
| 2× RTX 5080 | 32 GB | ~$2,500 | PCIe x8/x8 | ~$5,000 | ~$6,700 |
| 1× RTX 5090 | 32 GB | ~$3,000 | n/a | ~$5,000 | ~$6,700 |
| 2× RTX 4090 | 48 GB | ~$4,500 (used) | PCIe x8/x8 | ~$7,500 | ~$10,000 |
| **2× RTX 5090** | 64 GB | ~$6,000 | PCIe x8/x8 | **~$10,500** | **~$14,000** |

Conclusion: **2× RTX 3090 is the only multi-GPU config that fits a $5k sell price**,
and DDR5 inflation means even a single 5090 overshoots $5k while breaking the
"ships with multiple GPUs" promise. Hence a two-SKU lineup.

---

## SKU 1 — Cloudless One ($5,000) — the hero / Founders Edition

**2× RTX 3090 (NVLink) · 48 GB VRAM · serves quantized 70B-class LLMs + heavy
image/video gen.** The attainable machine that *proves the thesis*.

**Finalized component list (~$5,000 build, mid-2026 prices):**

| Component | Specific pick | Why | ~Price |
|---|---|---|---|
| GPU ×2 | RTX 3090 24 GB (used, tested + burn-in) | 48 GB pooled + only consumer NVLink | $2,200 |
| NVLink bridge | 4-slot 3090 NVLink | ~112 GB/s GPU-to-GPU for TP | $130 |
| CPU | AMD Ryzen 9 9900X (12C/24T) | Ample for inference; not the bottleneck | $380 |
| Motherboard | ASUS ProArt X870E-Creator | Dual PCIe 5.0 **x8/x8** → 5090-upgrade-ready | $480 |
| RAM | 96 GB (2×48) DDR5-6000 EXPO | 2× VRAM + CPU-offload headroom | $700 |
| PSU | MSI MEG Ai1300P (1300 W, ATX 3.1, 12V-2x6) | Headroom for a future 3rd/replacement card | $250 |
| Case | Fractal Meshify 2 XL (E-ATX) | Airflow + slot spacing (anti-sandwich) | $220 |
| CPU cooler | Arctic Liquid Freezer III 360 | Exhausts CPU heat away from GPUs | $100 |
| Storage 1 | 2 TB Gen5 NVMe | Boot + hot/active model | $180 |
| Storage 2 | 4 TB Gen4 NVMe | Model library (sequential load → Gen4 fine) | $260 |
| Extras | 2× 140 mm intakes + cables | Positive-pressure airflow | $80 |
| **Total BOM** | | | **~$4,980** |

→ As a **product**, this BOM sells at ~$6,500 for a healthy margin, or near-cost at $5k
as a **Founders Edition**. As a **machine you build**, ~$5k buys all of the above
(48 GB VRAM + 96 GB RAM + 6 TB storage). Lever: 64 GB RAM (~$400) drops it to ~$4,680.

> ⚠️ **Pricing correction (2026-06-28):** earlier drafts used ~$800/card. The 3090 is
> **EOL since late 2022** — *no new production*; supply is used (~$900–1,200/card) +
> dwindling new-old-stock (~$1,700–1,800, with aged-capacitor risk). Budget
> **~$2,200/pair** and treat supply as finite.

**Runs the target models:**
- **Qwen3.6-35B-A3B** (INT4 ~20 GB) — fits the 48 GB pool with huge KV headroom (hybrid
  linear attention keeps KV tiny) → long context, ~100–150 tok/s.
- **Qwen-Image** (20.4 B DiT + 8.3 B encoder) — *switch mode* gives it the whole box (DiT
  on GPU0, encoder on GPU1) → near-full quality; 3090s are fast at diffusion.

**Creative design choices:**
- **Two software modes** — *Big Brain* (TP=2 → one big/fast LLM) and *Studio* (partition:
  LLM on GPU0 + ComfyUI on GPU1, concurrent). Software switch over the existing per-GPU
  pinning ([[ARCHITECTURE]] Layer 1) — see proposed D33 (pool vs partition).
- **Tiered storage** — Gen5 hot drive + cheap Gen4 library; spend speed-money only on
  random I/O, not sequential weight loads.
- **Upgrade path built in** — ProArt x8/x8 + 1300 W PSU let a customer add a 5090 later
  and grow toward the Studio with no rebuild. The $5k box is the floor, not a dead end.
- **Default power-limit profile** — cap each 3090 to ~300 W (≈5% throughput loss) →
  quiet, cool, stays under a normal US 15A circuit. Ship as the appliance default.

**Why these choices**
- **3090 is PCIe Gen4 + NVLink** → NVLink carries TP traffic, so PCIe width barely matters
  for the cards themselves; the Gen5 ProArt board is bought purely for the *upgrade path*.
- **Ampere (sm_86) software is rock-solid** — vLLM/SGLang ([[DECISIONS]] D12) are more
  battle-tested on Ampere than the Blackwell path we're still validating. Lower risk.

**Honest risk + the new-parts alternative:** SKU 1 rests on the **used 3090 market** —
finite supply, no warranty, aging components (see correction above). Right for a
**limited Founders run or personal flagship**, not an infinitely-reorderable line. If
sustainable new-parts supply is required, the same ~$5k instead buys a **single RTX 5090
(32 GB, native FP8)** build — newer/future-proof and reorderable, but half the VRAM and
no concurrent-dual-model trick. For *max capability per dollar today*, 2× 3090 wins; for
a *scalable product*, the 5090 (or waiting for Blackwell prices to ease) is the play.

---

## SKU 2 — Cloudless Studio (~$13,000) — the halo, all-new-parts

**2× RTX 5090 (64 GB VRAM) · full x16/x16 · maximum new-parts VRAM.** For users who
want the most capable single-box local AI and a full warranty. Priced at true cost;
this is a halo unit, not a volume product.

| Component | Pick | ~Price |
|---|---|---|
| GPU ×2 | RTX 5090 32 GB (new) | $6,000 |
| CPU | Threadripper 9960X (24C) — for full x16/x16 lanes | $1,419 |
| Motherboard | ASUS Pro WS TRX50-SAGE WiFi (x16/x16, slot spacing, RDIMM) | $850 |
| RAM | 128 GB (4×32) DDR5-5600 ECC RDIMM *(spiked)* | $1,800 |
| PSU | Seasonic Prime TX-1600 (1600W Titanium, 2× native 12V-2x6) | $500 |
| Case | Corsair 7000D Airflow (E-ATX full tower) | $280 |
| CPU cooler | 360mm AIO (TR mount) | $120 |
| Storage | 4 TB Gen5 boot + 4 TB Gen4 model library | $780 |
| Fans / cables / assembly | | $150 |
| **Total BOM** | | **~$11,900** |

→ **Sell ~$13,000** (halo margin can run thinner in %; the value is the brand-topping
capability).

**Why Threadripper here:** the 5090 has **no NVLink**, so PCIe *is* the only
interconnect — full **x16/x16 Gen5** materially helps TP, and Threadripper's lane count
also fixes the two-thick-GPU slot-spacing/thermal problem. A **cost-down variant** on
AM5 (Ryzen 9 9950X + ASUS ProArt X870E-Creator at x8/x8 Gen5, non-ECC RAM) saves
~$1,300 on the platform but halves inter-GPU PCIe bandwidth and tightens GPU spacing —
acceptable for a cheaper "Studio" but not the reference build.

**Validated software path:** vLLM/SGLang are confirmed on the 5090 / Blackwell sm_120
([[DECISIONS]] D12, D15) — no software risk for the engine layer.

---

## Cross-SKU engineering caveats

1. **North American wall power.** The Studio's ~1,720W wall draw exceeds the **~1,440W
   continuous limit of a US 15A circuit** (NEC 80% rule). It needs a **dedicated 20A or
   240V line**, or software GPU power-limiting (~450–500W/card costs little inference
   throughput). Cloudless One at ~1000W stays safely under a standard circuit — a real
   advantage to call out in marketing.
2. **The two-GPU thermal sandwich is the #1 reliability risk.** Two triple-slot cards
   mounted ~2 slots apart starve and cook each other. **Solve it with motherboard slot
   spacing first**, case airflow second; consider water-cooled GPUs for the Studio.
3. **RAM is the budget wildcard.** 128 GB DDR5 went from ~$350 (2024) to **~$2,100**
   (2026). The rule of thumb is system RAM ≈ 2× total VRAM (staging/convert/mmap/offload).
   For Cloudless One, 64 GB is the pragmatic floor for 48 GB VRAM; revisit if DDR5 falls.
4. **Single-engine invariant holds on every SKU** ([[DECISIONS]] D15): one engine, one
   port, all GPUs feeding it via `--tensor-parallel-size N`. The planned enhancement to
   set TP size from `hardware.GPUs()` count is what makes both these dual-GPU SKUs
   actually use both cards — validate it on the 3-GPU box before shipping hardware.

## Open hardware questions

- **Warranty story for used 3090s** (SKU 1) — testing/burn-in process, RMA pool, term length.
- **Single source vs. self-assembly** — contract a boutique integrator vs. in-house build?
- **GPU cooling for the Studio** — air with wide slot spacing vs. AIO-hybrid vs. full custom loop.
- **A true mid-tier?** If 5090 prices fall toward MSRP, a 2× 5090 box could become the
  ~$7–8k "Pro" that bridges One and Studio.

## See also

- [[VISION]] — multiple-GPUs-scale-one-engine product thesis
- [[DECISIONS]] — D15 (single-engine invariant, TP scaling), D12 (vLLM/Blackwell)
- [[ROADMAP]] — Phase 2 hardware milestones
- [[ARCHITECTURE]] — Layer 2 (hardware enablement) this hardware must self-configure

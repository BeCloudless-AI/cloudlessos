import argparse
import asyncio
import base64
import io
import json
import threading
import time
import uuid

import torch
import uvicorn
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse, StreamingResponse
from PIL import Image
from transformers import AutoModel, AutoProcessor, AutoTokenizer


class LocateAnythingWorker:
    """NVIDIA's reviewed Transformers inference path, exposed by Cloudless."""

    def __init__(self, model_path: str, revision: str):
        self.device = "cuda"
        self.dtype = torch.bfloat16
        common = {"revision": revision, "trust_remote_code": True}
        self.tokenizer = AutoTokenizer.from_pretrained(model_path, **common)
        self.processor = AutoProcessor.from_pretrained(model_path, **common)
        self.model = AutoModel.from_pretrained(
            model_path, torch_dtype=self.dtype, **common
        ).to(self.device).eval()
        self.lock = threading.Lock()

    @torch.no_grad()
    def predict(self, image: Image.Image, question: str, body: dict) -> str:
        messages = [{"role": "user", "content": [
            {"type": "image", "image": image},
            {"type": "text", "text": question},
        ]}]
        text = self.processor.py_apply_chat_template(
            messages, tokenize=False, add_generation_prompt=True
        )
        images, videos = self.processor.process_vision_info(messages)
        inputs = self.processor(
            text=[text], images=images, videos=videos, return_tensors="pt"
        ).to(self.device)
        with self.lock:
            response = self.model.generate(
                pixel_values=inputs["pixel_values"].to(self.dtype),
                input_ids=inputs["input_ids"],
                attention_mask=inputs["attention_mask"],
                image_grid_hws=inputs.get("image_grid_hws"),
                tokenizer=self.tokenizer,
                max_new_tokens=min(int(body.get("max_tokens", 2048)), 8192),
                use_cache=True,
                generation_mode=body.get("generation_mode", "hybrid"),
                temperature=float(body.get("temperature", 0.7)),
                do_sample=True,
                top_p=float(body.get("top_p", 0.9)),
                repetition_penalty=1.1,
                verbose=False,
            )
        answer = response[0] if isinstance(response, tuple) else response
        return str(answer)


def data_image(value: str) -> Image.Image:
    if not value.startswith("data:image/") or "," not in value:
        raise HTTPException(
            status_code=400,
            detail="LocateAnything accepts base64 data image URLs; remote URLs are disabled.",
        )
    try:
        payload = value.split(",", 1)[1]
        return Image.open(io.BytesIO(base64.b64decode(payload))).convert("RGB")
    except Exception as exc:
        raise HTTPException(status_code=400, detail="The image payload is invalid.") from exc


def prompt_and_image(body: dict):
    texts = []
    image = None
    for message in body.get("messages", []):
        content = message.get("content", "")
        if isinstance(content, str):
            texts.append(content)
            continue
        for part in content or []:
            if part.get("type") == "text":
                texts.append(str(part.get("text", "")))
            elif part.get("type") == "image_url":
                value = part.get("image_url", "")
                if isinstance(value, dict):
                    value = value.get("url", "")
                image = data_image(str(value))
    if image is None:
        raise HTTPException(status_code=400, detail="LocateAnything requires an image.")
    question = "\n".join(text for text in texts if text.strip()).strip()
    if not question:
        raise HTTPException(status_code=400, detail="LocateAnything requires a text instruction.")
    return question, image


def completion_payload(served_name: str, content: str, request_id: str):
    return {
        "id": request_id,
        "object": "chat.completion",
        "created": int(time.time()),
        "model": served_name,
        "choices": [{"index": 0, "message": {"role": "assistant", "content": content}, "finish_reason": "stop"}],
        "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
    }


def create_app(worker: LocateAnythingWorker, served_name: str):
    app = FastAPI(title="Cloudless LocateAnything adapter", docs_url=None, redoc_url=None)

    @app.get("/health")
    async def health():
        return {"status": "ok", "model": served_name}

    @app.get("/v1/models")
    async def models():
        return {"object": "list", "data": [{"id": served_name, "object": "model", "owned_by": "cloudless"}]}

    @app.post("/v1/chat/completions")
    async def completions(request: Request):
        body = await request.json()
        question, image = prompt_and_image(body)
        content = await asyncio.to_thread(worker.predict, image, question, body)
        request_id = "chatcmpl-" + uuid.uuid4().hex
        if not body.get("stream"):
            return JSONResponse(completion_payload(served_name, content, request_id))

        async def events():
            chunk = {
                "id": request_id,
                "object": "chat.completion.chunk",
                "created": int(time.time()),
                "model": served_name,
                "choices": [{"index": 0, "delta": {"role": "assistant", "content": content}, "finish_reason": None}],
            }
            yield "data: " + json.dumps(chunk) + "\n\n"
            chunk["choices"][0] = {"index": 0, "delta": {}, "finish_reason": "stop"}
            yield "data: " + json.dumps(chunk) + "\n\n"
            yield "data: [DONE]\n\n"

        return StreamingResponse(events(), media_type="text/event-stream")

    return app


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--served-model-name", default="cloudless")
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8000)
    args = parser.parse_args()
    worker = LocateAnythingWorker(args.model, args.revision)
    uvicorn.run(create_app(worker, args.served_model_name), host=args.host, port=args.port)


if __name__ == "__main__":
    main()

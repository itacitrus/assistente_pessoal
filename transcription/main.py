import os
import tempfile

import assemblyai as aai
from fastapi import FastAPI, UploadFile, File, HTTPException

app = FastAPI(title="Transcription API")

aai.settings.api_key = os.environ.get("ASSEMBLYAI_API_KEY", "")


@app.post("/transcribe")
async def transcribe(file: UploadFile = File(...)):
    if not aai.settings.api_key:
        raise HTTPException(status_code=500, detail="ASSEMBLYAI_API_KEY not configured")

    suffix = os.path.splitext(file.filename or "audio.ogg")[1]
    with tempfile.NamedTemporaryFile(delete=False, suffix=suffix) as tmp:
        content = await file.read()
        tmp.write(content)
        tmp_path = tmp.name

    try:
        config = aai.TranscriptionConfig(
            language_code="pt",
            speech_models=["universal-3-pro"],
        )
        transcriber = aai.Transcriber()
        transcript = transcriber.transcribe(tmp_path, config=config)

        if transcript.status == aai.TranscriptStatus.error:
            raise HTTPException(status_code=500, detail=f"Transcription failed: {transcript.error}")

        return {"text": transcript.text or ""}
    finally:
        os.unlink(tmp_path)


@app.get("/health")
async def health():
    return {"status": "ok"}

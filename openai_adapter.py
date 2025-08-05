import os
import asyncio
import json
import base64
import tempfile
from fastapi import FastAPI, Request, HTTPException, Header
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, Field
from typing import List, Optional, Union, Any, Dict
import uvicorn
import uuid
import time
from dotenv import load_dotenv

from gemini_webapi import GeminiClient
from gemini_webapi.constants import Model

# --- Pydantic Models for OpenAI Compatibility ---

class ChatMessage(BaseModel):
    role: str
    content: Union[str, List[Dict[str, Any]]]

class ChatCompletionRequest(BaseModel):
    model: str
    messages: List[ChatMessage]
    stream: Optional[bool] = False

class ChatCompletionChoice(BaseModel):
    index: int = 0
    message: ChatMessage
    finish_reason: str = "stop"

class Usage(BaseModel):
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0

class ChatCompletionResponse(BaseModel):
    id: str = Field(default_factory=lambda: f"chatcmpl-{uuid.uuid4().hex}")
    object: str = "chat.completion"
    created: int = Field(default_factory=lambda: int(time.time()))
    model: str
    choices: List[ChatCompletionChoice]
    usage: Usage = Field(default_factory=Usage)
    
# --- Pydantic Models for /v1/models ---

class ModelCard(BaseModel):
    id: str
    object: str = "model"
    created: int = Field(default_factory=lambda: int(time.time()))
    owned_by: str = "Google"

class ModelList(BaseModel):
    object: str = "list"
    data: List[ModelCard] = []

# --- Pydantic Models for Streaming ---

class DeltaMessage(BaseModel):
    role: Optional[str] = None
    content: Optional[str] = None

class ChatCompletionStreamChoice(BaseModel):
    index: int = 0
    delta: DeltaMessage
    finish_reason: Optional[str] = None

class ChatCompletionStreamResponse(BaseModel):
    id: str = Field(default_factory=lambda: f"chatcmpl-{uuid.uuid4().hex}")
    object: str = "chat.completion.chunk"
    created: int = Field(default_factory=lambda: int(time.time()))
    model: str
    choices: List[ChatCompletionStreamChoice]

# --- FastAPI Application ---

app = FastAPI(title="Gemini to OpenAI Adapter", version="1.0")

# --- Environment and Gemini Client Setup ---

gemini_client = None

@app.on_event("startup")
async def startup_event():
    global gemini_client
    print("Loading environment variables from .env file...")
    load_dotenv(dotenv_path=".env")

    secure_1psid = os.getenv("__Secure-1PSID")
    secure_1psidts = os.getenv("__Secure-1PSIDTS")

    if not secure_1psid or not secure_1psidts:
        print("FATAL: Cookies not found in .env file.")
        print("Please run get_cookies.py first, or fill .env manually.")
        return

    print("Initializing Gemini Client...")
    try:
        gemini_client = GeminiClient(secure_1psid, secure_1psidts)
        await gemini_client.init(timeout=180)
        print("Gemini Client initialized successfully.")
    except Exception as e:
        gemini_client = None
        print(f"FATAL: Failed to initialize Gemini Client: {e}")
        print("Your cookies in .env might be expired. Please run get_cookies.py to refresh them.")

# --- API Endpoints ---

@app.get("/v1/models", response_model=ModelList)
async def list_models():
    """
    Handler for OpenAI-compatible model listing.
    """
    model_cards = [ModelCard(id=model.value[0]) for model in Model if model != Model.UNSPECIFIED]
    return ModelList(data=model_cards)

@app.post("/v1/chat/completions")
async def chat_completions(request: ChatCompletionRequest, authorization: Optional[str] = Header(None)):
    if gemini_client is None:
        raise HTTPException(status_code=503, detail="Gemini Client not initialized. Please check server logs.")

    prompt_text_parts = []
    image_files = []
    temp_files = []

    # Process messages to extract text and images
    for message in request.messages:
        if isinstance(message.content, str):
            prompt_text_parts.append(message.content)
        elif isinstance(message.content, list):
            for part in message.content:
                if part.get("type") == "text":
                    prompt_text_parts.append(part.get("text", ""))
                elif part.get("type") == "image_url":
                    image_data = part.get("image_url", {}).get("url")
                    if image_data and image_data.startswith("data:image/"):
                        header, encoded = image_data.split(",", 1)
                        file_ext = header.split("/")[1].split(";")[0]
                        
                        with tempfile.NamedTemporaryFile(delete=False, suffix=f".{file_ext}") as fp:
                            fp.write(base64.b64decode(encoded))
                            image_files.append(fp.name)
                            temp_files.append(fp.name)

    final_prompt = "\n".join(prompt_text_parts)
    if not final_prompt and image_files:
        final_prompt = "Describe the image(s)."

    chat_session = gemini_client.start_chat(model=request.model)

    async def stream_generator():
        try:
            response = await chat_session.send_message(final_prompt, files=image_files)
            
            candidate = response.candidates[0]
            thought_part = candidate.thoughts
            completion_part = candidate.text

            full_content = ""
            if thought_part:
                cleaned_thoughts = "\n".join([line.strip() for line in "".join(thought_part).splitlines() if line.strip()])
                full_content += f"<thought>{cleaned_thoughts}</thought>"

            if completion_part:
                full_content += completion_part

            if full_content:
                role_chunk = ChatCompletionStreamResponse(
                    model=request.model,
                    choices=[ChatCompletionStreamChoice(delta=DeltaMessage(role="assistant"))]
                )
                yield f"data: {role_chunk.model_dump_json()}\n\n"
                
                content_chunk = ChatCompletionStreamResponse(
                    model=request.model,
                    choices=[ChatCompletionStreamChoice(delta=DeltaMessage(content=full_content))]
                )
                yield f"data: {content_chunk.model_dump_json()}\n\n"

            final_chunk = ChatCompletionStreamResponse(
                model=request.model,
                choices=[ChatCompletionStreamChoice(delta=DeltaMessage(), finish_reason="stop")]
            )
            yield f"data: {final_chunk.model_dump_json()}\n\n"
            yield "data: [DONE]\n\n"

        except Exception as e:
            print(f"Error during stream generation: {e}")
            error_content = f"Error: {str(e)}"
            error_chunk = ChatCompletionStreamResponse(
                model=request.model,
                choices=[ChatCompletionStreamChoice(delta=DeltaMessage(content=error_content), finish_reason="error")]
            )
            yield f"data: {error_chunk.model_dump_json()}\n\n"
            yield "data: [DONE]\n\n"
        finally:
            # Clean up temporary files
            for temp_file in temp_files:
                try:
                    os.remove(temp_file)
                except OSError as e:
                    print(f"Error removing temporary file {temp_file}: {e}")

    if request.stream:
        return StreamingResponse(stream_generator(), media_type="text/event-stream")
    else:
        try:
            response = await chat_session.send_message(final_prompt, files=image_files)
            candidate = response.candidates[0]
            thought_part = candidate.thoughts
            completion_part = candidate.text

            full_content = ""
            if thought_part:
                cleaned_thoughts = "\n".join([line.strip() for line in "".join(thought_part).splitlines() if line.strip()])
                full_content += f"<thought>{cleaned_thoughts}</thought>"
            
            if completion_part:
                full_content += completion_part

            return ChatCompletionResponse(
                model=request.model,
                choices=[ChatCompletionChoice(message=ChatMessage(role="assistant", content=full_content))]
            )
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))
        finally:
            # Clean up temporary files
            for temp_file in temp_files:
                try:
                    os.remove(temp_file)
                except OSError as e:
                    print(f"Error removing temporary file {temp_file}: {e}")

@app.get("/")
def read_root():
    return {"message": "Gemini to OpenAI Adapter is running. Post to /v1/chat/completions to use."}

if __name__ == "__main__":
    print("Starting Gemini to OpenAI Adapter server...")
    uvicorn.run(app, host="0.0.0.0", port=8000)
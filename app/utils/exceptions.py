from typing import Any, Callable
from fastapi import HTTPException
from config import logger as logging
from uptime_kuma_api import UptimeKumaException
import asyncio


async def handle_api_exceptions(func: Callable[..., Any], *args, **kwargs) -> Any:
    try:
        if asyncio.iscoroutinefunction(func):
            result = await func(*args, **kwargs)
        else:
            result = func(*args, **kwargs)
        return result
    except UptimeKumaException as e:
        logging.error(e)
        raise HTTPException(status_code=400, detail=str(e))
    except Exception as e:
        logging.fatal(e)
        raise HTTPException(status_code=500, detail="Unexpected Error: " + str(e))

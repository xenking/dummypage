FROM python:3.7-alpine
WORKDIR /code
RUN apk update \
    && apk add --update --no-cache gcc musl-dev linux-headers alpine-sdk
COPY requirements.txt requirements.txt
RUN pip install -U setuptools \
    && pip install -r requirements.txt
COPY app ./app
#EXPOSE 5000:5000
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "5000"]
#ENTRYPOINT ["/bin/sh"]
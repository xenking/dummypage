from quart import Quart, render_template, abort, send_file, safe_join, Response
from quart.exceptions import NotFound
from .config import HOST, PORT
from pathlib import Path

app = Quart(__name__, static_folder='static', static_url_path='')
app.config['JSONIFY_PRETTYPRINT_REGULAR'] = False

# Register the blueprints
app.debug = True


@app.route("/")
async def index():
    return await render_template("index.html")


@app.route("/cfg/", defaults={'req_path': ""})
@app.route("/cfg/<path:req_path>")
async def browsecfgs(req_path):
    base_path = Path('/site/cfgs').resolve(strict=True)
    dirs = []
    files = []
    # Joining the base and the requested path
    print(req_path)
    abs_path = base_path.joinpath(req_path)
    # Return 404 if path doesn't exist
    if not base_path.exists() or not abs_path.exists():
        return await abort(404)

    # Check if path is a file and serve
    if abs_path.is_file():
        return await send_file(abs_path)

    if abs_path.is_dir():
        # Show directory contents
        for el in abs_path.iterdir():
            rel_path = "/".join(el.parts[3:])
            if el.is_dir():
                dirs.append(rel_path)
            if el.is_file():
                files.append(rel_path)
        return await render_template('browser.html', files=files, dirs=dirs)


@app.route('/cfg/<path:filename>/raw')
async def open_file_raw(filename):
    filename = safe_join('/site/cfgs', filename)
    try:
        with open(filename, "rb") as fd:
            lines = b''.join(line for line in fd)

        return Response(lines, status=200, content_type="text/plain; charset=utf-8")
        # return await render_template('file.html', file=filename, strings=lines)
    except NotFound:
        return await abort(404)


@app.errorhandler(404)
async def page_not_found(error):
    return await render_template("404.html"), 404


if __name__ == '__main__':
    app.run(host=HOST, port=PORT)

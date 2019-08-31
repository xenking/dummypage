from quart import Quart, render_template
from .config import HOST, PORT

app = Quart(__name__, static_folder='static', static_url_path='')
app.config['JSONIFY_PRETTYPRINT_REGULAR'] = False

# Register the blueprints
app.debug = True


@app.route("/")
async def index():
    return await render_template("index.html")


@app.errorhandler(404)
async def page_not_found(error):
    return await render_template("404.html"), 404


if __name__ == '__main__':
    app.run(host=HOST, port=PORT)
